// Package codeidx 提供纯 Go 的轻量代码索引：不依赖 ctags / LSP / 三方库，
// 只用标准库解析源码抽取符号（函数、方法、类型、类等），支持磁盘缓存与增量更新。
// 目标是在老旧 Win7 机器上也能秒级建立工作区符号索引。
package codeidx

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"win7harness/internal/core"
)

// 索引规模上限：超过就截断，保证在低配机器上不会把内存吃满。
// 这里写的是"标准档"的基线，实际生效值由 buildLimits 按内存档位决定。
const (
	maxFiles   = 3000
	maxSymbols = 20000
	maxFileSz  = 1 << 20 // 单文件 1MB 以上跳过，多半是生成代码
	// symOverhead 是单个 Symbol 除字符串内容外的固定开销估算：
	// 4 个 string header(64B) + 3 个 int(24B) + 对齐，取 96B 便于预算换算。
	symOverhead = 96
)

// buildLimits 返回当前内存档位下索引的文件数与符号数上限。
// 低内存档（256MB 友好）会明显收紧：32000 个符号的索引在 32 位进程里并不便宜。
func buildLimits() (int, int) {
	m := core.MemProfile()
	f, sy := m.IndexFiles, m.IndexSymbols
	if f <= 0 || f > maxFiles {
		f = maxFiles
	}
	if sy <= 0 || sy > maxSymbols {
		sy = maxSymbols
	}
	return f, sy
}

// EstimateBytes 估算索引常驻内存（字节），用于 /index 回显与峰值自检。
// 只算大头：符号结构体 + 字符串内容，文件表按 map 每项约 96B 估。
func (ix *Index) EstimateBytes() int64 {
	if ix == nil {
		return 0
	}
	var n int64
	n += int64(len(ix.Files)) * 96
	n += int64(len(ix.Sizes)) * 96
	for _, s := range ix.Syms {
		n += symOverhead + int64(len(s.Name)+len(s.Kind)+len(s.Recv)+len(s.File)+len(s.Sig))
	}
	return n
}

// Symbol 是一条符号记录。
type Symbol struct {
	Name string `json:"n"`
	Kind string `json:"k"` // func / method / type / const / var / class / def / struct / interface
	Recv string `json:"r,omitempty"`
	File string `json:"f"` // 相对 root，/ 分隔
	Line int    `json:"l"`
	Sig  string `json:"s,omitempty"`
}

// Index 是某个工作区的符号索引快照。
type Index struct {
	Schema    int              `json:"schema"`
	Rules     string           `json:"rules"`
	Root      string           `json:"root"`
	BuiltAt   time.Time        `json:"built_at"`
	Files     map[string]int64 `json:"files"` // rel -> mtimeUnixNano
	Sizes     map[string]int64 `json:"sizes"` // rel -> 字节数
	Syms      []Symbol         `json:"syms"`
	Truncated bool             `json:"truncated"`
	LastBuild BuildStats       `json:"last_build,omitempty"`
}

// BuildStats 记录一次建索引的增量效果，供 /index 与自检回显。
type BuildStats struct {
	Total       int   `json:"total"`
	Reused      int   `json:"reused"`
	Parsed      int   `json:"parsed"`
	Dirty       int   `json:"dirty"`
	Removed     int   `json:"removed"`
	Incremental bool  `json:"incremental"`
	ElapsedMs   int64 `json:"elapsed_ms"`
	// ParseErrs 是解析失败的文件数（语法残缺时正常，用户正在改）；
	// SkippedFiles 是这些文件的相对路径（最多 20 条），便于用户知道哪些被放弃了。
	ParseErrs    int      `json:"parse_errs,omitempty"`
	SkippedFiles []string `json:"skipped_files,omitempty"`
	// MemBytes 建完后的索引常驻内存估算；BudgetBytes 是本次生效的符号预算。
	MemBytes    int64 `json:"mem_bytes,omitempty"`
	BudgetBytes int64 `json:"budget_bytes,omitempty"`
}

var skipDirs = map[string]bool{
	".git": true, ".svn": true, ".hg": true, ".idea": true, ".vscode": true,
	"node_modules": true, "vendor": true, "__pycache__": true, ".venv": true,
	"dist": true, "build": true, "target": true, ".cache": true, ".tox": true,
	"bin": true, "obj": true, "out": true,
}

var goExt = map[string]bool{".go": true}
var genericExt = map[string]bool{
	".py": true, ".js": true, ".mjs": true, ".ts": true, ".tsx": true, ".jsx": true,
	".java": true, ".c": true, ".h": true, ".cc": true, ".cpp": true, ".hpp": true,
	".rs": true, ".rb": true, ".php": true, ".cs": true, ".kt": true, ".swift": true,
	".lua": true, ".sh": true,
}

var (
	mu      sync.RWMutex
	current *Index
)

// ---- 脏文件登记 ----
// 工具写入/编辑文件后登记脏文件，下次增量重建时即使 mtime 精度不足也会强制重解析。

var (
	dirtyMu   sync.Mutex
	dirtySet  = map[string]bool{}
	timerMu   sync.Mutex
	timerOn   bool
	forceMu   sync.Mutex
	forceFull bool
)

func takeForce() bool {
	forceMu.Lock()
	defer forceMu.Unlock()
	v := forceFull
	forceFull = false
	return v
}

func normKey(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

// MarkDirtyAbs 登记一个被写过的绝对路径。
// 调用图（calls.go 的 callers/callees）是按进程内缓存进程外由 ResetGraph 失效的，
// 必须在这里同步失效——否则编辑后查调用点，拿到的还是编辑前的关系。
func MarkDirtyAbs(abs string) {
	if abs == "" {
		return
	}
	dirtyMu.Lock()
	dirtySet[normKey(abs)] = true
	dirtyMu.Unlock()
	ResetGraph()
}

// MarkDirtyRel 按 root + 相对路径登记（相对路径用 / 分隔）。
func MarkDirtyRel(root, rel string) {
	if root == "" || rel == "" {
		return
	}
	MarkDirtyAbs(filepath.Join(root, filepath.FromSlash(rel)))
}

// DirtyCount 当前待增量重建的脏文件数。
func DirtyCount() int {
	dirtyMu.Lock()
	defer dirtyMu.Unlock()
	return len(dirtySet)
}

func takeDirty() map[string]bool {
	dirtyMu.Lock()
	defer dirtyMu.Unlock()
	if len(dirtySet) == 0 {
		return nil
	}
	out := dirtySet
	dirtySet = map[string]bool{}
	return out
}

// Touch 登记脏文件并在后台（短暂合并）执行一次增量重建，写完文件后调用即可。
func Touch(abs string) {
	MarkDirtyAbs(abs)
	scheduleIncremental(300 * time.Millisecond)
}

func scheduleIncremental(d time.Duration) {
	timerMu.Lock()
	if timerOn {
		timerMu.Unlock()
		return
	}
	timerOn = true
	timerMu.Unlock()
	time.AfterFunc(d, func() {
		timerMu.Lock()
		timerOn = false
		timerMu.Unlock()
		if root := currentRoot(); root != "" {
			_ = Update(root)
		}
	})
}

// MarkStale 用于无法精确定位改动文件的场景（如批量补丁、执行命令改了文件），
// 下一轮后台重建改为全量，保证符号与行号不会过期。
func MarkStale() {
	forceMu.Lock()
	forceFull = true
	forceMu.Unlock()
	ResetGraph()
	scheduleIncremental(300 * time.Millisecond)
}

// Update 基于当前内存索引做一次增量重建（只重解析变化/脏文件），返回统计。
func Update(root string) BuildStats {
	prev := Cached()
	if takeForce() {
		prev = nil // 全量重建
	}
	ix := Build(root, prev)
	setCurrent(ix)
	saveCache(ix)
	return ix.LastBuild
}

func currentRoot() string {
	if ix := Cached(); ix != nil {
		return ix.Root
	}
	return ""
}

// errStopWalk 用于在文件数超限时提前结束遍历。
var errStopWalk = errors.New("codeidx: file limit reached")

// Cached 返回当前内存中的索引（可能为 nil），供界面线程无阻塞读取。
func Cached() *Index {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

func setCurrent(ix *Index) {
	mu.Lock()
	current = ix
	mu.Unlock()
}

// Reset 清空内存索引与脏标记（评测与测试用，保证用例互不影响）。
func Reset() {
	mu.Lock()
	current = nil
	mu.Unlock()
	dirtyMu.Lock()
	dirtySet = map[string]bool{}
	dirtyMu.Unlock()
	forceMu.Lock()
	forceFull = false
	forceMu.Unlock()
}

// Build 扫描 root 建立符号索引。prev 为上次索引（可为 nil），未变化的文件直接复用旧符号。
func Build(root string, prev *Index) *Index {
	root = filepath.Clean(root)
	fileLimit, symLimit := buildLimits()
	ix := &Index{
		Schema:  CacheSchema,
		Rules:   RulesVersion,
		Root:    root,
		BuiltAt: time.Now(),
		Files:   map[string]int64{},
		Sizes:   map[string]int64{},
	}
	if prev == nil || !samePath(prev.Root, root) {
		prev = nil
	}

	// 旧符号按文件分组，便于未变文件直接搬运。
	prevByFile := map[string][]Symbol{}
	if prev != nil {
		for _, s := range prev.Syms {
			prevByFile[s.File] = append(prevByFile[s.File], s)
		}
	}

	var files []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || p == root {
			return nil
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= fileLimit {
			ix.Truncated = true
			return errStopWalk
		}
		ext := strings.ToLower(filepath.Ext(p))
		if !goExt[ext] && !genericExt[ext] {
			return nil
		}
		if info.Size() > maxFileSz {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		files = append(files, rel)
		ix.Files[rel] = info.ModTime().UnixNano()
		ix.Sizes[rel] = info.Size()
		return nil
	})
	sort.Strings(files)

	dirty := takeDirty()
	st := BuildStats{Total: len(files), Incremental: prev != nil}
	// 峰值控制的第一招：所有文件都没变化时直接复用上一份快照。
	// 少了这一条，每次后台重建都要同时持有新旧两份符号表，
	// 大仓库上这就是"内存突然翻倍"的来源。
	if prev != nil && len(dirty) == 0 && !takeForce() && sameFileSet(prev, ix) {
		prev.BuiltAt = ix.BuiltAt
		prev.LastBuild = BuildStats{
			Total: len(files), Reused: len(files), Incremental: true,
			ElapsedMs:   time.Since(ix.BuiltAt).Milliseconds(),
			MemBytes:    prev.EstimateBytes(),
			BudgetBytes: int64(symLimit) * symOverhead,
		}
		return prev
	}
	if prev != nil {
		for rel := range prev.Files {
			if _, ok := ix.Files[rel]; !ok {
				st.Removed++
			}
		}
	}

	// 按上次符号数预分配，避免 append 反复翻倍扩容（扩容瞬间也是双份内存）。
	capHint := symLimit
	if prev != nil && len(prev.Syms) < capHint {
		capHint = len(prev.Syms)
	}
	if capHint < 1024 {
		capHint = 1024
	}
	ix.Syms = make([]Symbol, 0, capHint)
	budget := int64(symLimit) * symOverhead
	intern := map[string]string{}

	for _, rel := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		// 脏文件（刚被工具写过）即使 mtime/大小都没变也强制重解析。
		isDirty := dirty != nil && dirty[normKey(abs)]
		// 只有 mtime 与大小都没变、且不是脏文件，才复用旧符号。
		if !isDirty {
			if syms, ok := prevByFile[rel]; ok && prev != nil &&
				prev.Files[rel] == ix.Files[rel] && prev.Sizes[rel] == ix.Sizes[rel] {
				for _, sym := range syms {
					sym.File = internStr(intern, sym.File)
					sym.Kind = internStr(intern, sym.Kind)
					ix.Syms = append(ix.Syms, sym)
				}
				st.Reused++
				if len(ix.Syms) >= symLimit {
					break
				}
				continue
			}
		} else {
			st.Dirty++
		}
		ext := strings.ToLower(filepath.Ext(rel))
		var got []Symbol
		var perr error
		if goExt[ext] {
			got, perr = parseGoFile(abs, rel)
			if perr != nil {
				st.ParseErrs++
				if len(st.SkippedFiles) < 20 {
					st.SkippedFiles = append(st.SkippedFiles, rel)
				}
			}
		} else {
			got = parseGenericFile(abs, rel)
		}
		for _, sym := range got {
			if len(ix.Syms) >= symLimit {
				ix.Truncated = true
				break
			}
			// 同一文件里的符号共享一份 File 字符串，Kind 也只有几种：
			// 驻留后内存占用能压掉三成左右，而代码路径几乎不变。
			sym.File = internStr(intern, sym.File)
			sym.Kind = internStr(intern, sym.Kind)
			ix.Syms = append(ix.Syms, sym)
		}
		st.Parsed++
		if len(ix.Syms) >= symLimit {
			ix.Truncated = true
			break
		}
		// 预算兜底：符号数没超，但签名特别长（比如一堆巨长泛型）时也会撑爆，
		// 这里按实际字节数再卡一道。
		if int64(len(ix.Syms))*symOverhead > budget*2 {
			ix.Truncated = true
			break
		}
	}
	st.ElapsedMs = time.Since(ix.BuiltAt).Milliseconds()
	st.MemBytes = ix.EstimateBytes()
	st.BudgetBytes = budget
	ix.LastBuild = st
	sort.SliceStable(ix.Syms, func(i, j int) bool {
		if ix.Syms[i].File != ix.Syms[j].File {
			return ix.Syms[i].File < ix.Syms[j].File
		}
		return ix.Syms[i].Line < ix.Syms[j].Line
	})
	return ix
}

// sameFileSet 判断 prev 与 next 的文件集合、mtime、大小是否完全一致。
func sameFileSet(prev, next *Index) bool {
	if prev == nil || next == nil || len(prev.Files) != len(next.Files) {
		return false
	}
	for rel, mt := range next.Files {
		if pmt, ok := prev.Files[rel]; !ok || pmt != mt {
			return false
		}
		if prev.Sizes[rel] != next.Sizes[rel] {
			return false
		}
	}
	return true
}

// internStr 复用已存在的字符串，避免同一路径/同一种类被复制上千份。
func internStr(pool map[string]string, s string) string {
	if s == "" {
		return s
	}
	if v, ok := pool[s]; ok {
		return v
	}
	pool[s] = s
	return s
}

// BuildAsync 在后台建立索引并更新全局快照，界面线程不会被阻塞。
func BuildAsync(root string) {
	core.SafeGo("codeidx/index.go:454", func() {
		ix := Build(root, Cached())
		setCurrent(ix)
		saveCache(ix)
	})
}

// Refresh 强制重建索引（清掉旧快照）。
func Refresh(root string) *Index {
	ix := Build(root, nil)
	setCurrent(ix)
	saveCache(ix)
	return ix
}

func parseGoFile(abs, rel string) ([]Symbol, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, nil, parser.SkipObjectResolution)
	if err != nil || f == nil {
		if err == nil {
			err = errors.New("parseGoFile: 未知错误")
		}
		return nil, err
	}
	var out []Symbol
	add := func(name, kind, recv string, pos token.Pos, sig string) {
		if name == "" || name == "_" {
			return
		}
		out = append(out, Symbol{
			Name: name, Kind: kind, Recv: recv, File: rel,
			Line: fset.Position(pos).Line, Sig: strings.TrimSpace(sig),
		})
	}
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			kind := "func"
			recv := ""
			if decl.Recv != nil && len(decl.Recv.List) > 0 {
				kind = "method"
				recv = recvName(decl.Recv.List[0].Type)
			}
			add(decl.Name.Name, kind, recv, decl.Pos(), goFuncSig(decl))
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				switch sp := spec.(type) {
				case *ast.TypeSpec:
					kind := "type"
					switch sp.Type.(type) {
					case *ast.InterfaceType:
						kind = "interface"
					case *ast.StructType:
						kind = "struct"
					}
					add(sp.Name.Name, kind, "", sp.Pos(), "type "+sp.Name.Name)
				case *ast.ValueSpec:
					kind := "var"
					if decl.Tok == token.CONST {
						kind = "const"
					}
					for _, n := range sp.Names {
						add(n.Name, kind, "", n.Pos(), kind+" "+n.Name)
					}
				}
			}
		}
	}
	return out, nil
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return recvName(t.X)
	}
	return ""
}

// goFuncSig 只取首行签名，避免把整段函数体塞进索引。
func goFuncSig(d *ast.FuncDecl) string {
	var b strings.Builder
	b.WriteString("func ")
	if d.Recv != nil && len(d.Recv.List) > 0 {
		b.WriteString("(")
		b.WriteString(exprText(d.Recv.List[0].Type))
		b.WriteString(") ")
	}
	b.WriteString(d.Name.Name)
	b.WriteString("(")
	if d.Type.Params != nil {
		var parts []string
		for _, p := range d.Type.Params.List {
			t := exprText(p.Type)
			if len(p.Names) == 0 {
				parts = append(parts, t)
				continue
			}
			for _, n := range p.Names {
				parts = append(parts, n.Name+" "+t)
			}
		}
		b.WriteString(strings.Join(parts, ", "))
	}
	b.WriteString(")")
	if d.Type.Results != nil && len(d.Type.Results.List) > 0 {
		var parts []string
		for _, p := range d.Type.Results.List {
			parts = append(parts, exprText(p.Type))
		}
		if len(parts) == 1 {
			b.WriteString(" " + parts[0])
		} else {
			b.WriteString(" (" + strings.Join(parts, ", ") + ")")
		}
	}
	s := b.String()
	if len([]rune(s)) > 160 {
		s = string([]rune(s)[:159]) + "…"
	}
	return s
}

func exprText(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprText(t.X)
	case *ast.SelectorExpr:
		return exprText(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		if t.Len == nil {
			return "[]" + exprText(t.Elt)
		}
		return "[]" + exprText(t.Elt)
	case *ast.MapType:
		return "map[" + exprText(t.Key) + "]" + exprText(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func"
	case *ast.ChanType:
		return "chan " + exprText(t.Value)
	case *ast.Ellipsis:
		return "..." + exprText(t.Elt)
	case *ast.IndexExpr:
		return exprText(t.X) + "[" + exprText(t.Index) + "]"
	}
	return "?"
}

// 非 Go 语言走轻量正则：够用且快，不引入完整语法树。
var genericPatterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"class", regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?class\s+([A-Za-z_$][\w$]*)`)},
	{"func", regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`)},
	{"def", regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_][\w]*)`)},
	{"func", regexp.MustCompile(`^\s*(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_][\w]*)`)},
	{"func", regexp.MustCompile(`^(?:public|private|protected|static|internal|virtual|\s)*[\w<>\[\],\s]+?\s+([A-Za-z_][\w]*)\s*\([^;]*\)\s*\{?\s*$`)},
	{"struct", regexp.MustCompile(`^\s*(?:pub\s+)?(?:struct|interface|enum|trait)\s+([A-Za-z_][\w]*)`)},
	{"type", regexp.MustCompile(`^\s*type\s+([A-Za-z_][\w]*)\s+(?:struct|interface|=)`)},
	{"var", regexp.MustCompile(`^\s*(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=`)},
}

func parseGenericFile(abs, rel string) []Symbol {
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var out []Symbol
	for i, ln := range lines {
		if len(out) > 400 {
			break
		}
		trimmed := strings.TrimSpace(ln)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		for _, pat := range genericPatterns {
			m := pat.re.FindStringSubmatch(ln)
			if len(m) < 2 {
				continue
			}
			name := m[1]
			if isKeyword(name) || len(name) > 80 {
				continue
			}
			sig := trimmed
			if r := []rune(sig); len(r) > 120 {
				sig = string(r[:119]) + "…"
			}
			out = append(out, Symbol{Name: name, Kind: pat.kind, File: rel, Line: i + 1, Sig: sig})
			break
		}
	}
	return out
}

var keywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true, "return": true,
	"new": true, "else": true, "do": true, "try": true, "with": true,
}

func isKeyword(s string) bool {
	return keywords[s]
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// ---- 磁盘缓存 ----

func cachePath() string {
	return filepath.Join(core.LocalDataDir(), "codeindex.json")
}

// SaveTo 把索引写到指定路径（测试与自定义位置用）。
func (ix *Index) SaveTo(p string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	ix.Schema = CacheSchema
	ix.Rules = RulesVersion
	_, err := writeCacheFile(p, ix)
	return err
}

func saveCache(ix *Index) {
	_ = ix.SaveTo(cachePath())
}

// LoadFrom 读取缓存索引；损坏或不存在时返回 nil。
func LoadFrom(p string) *Index {
	data, compressed, err := readMaybeGzip(p)
	if err != nil || len(data) == 0 {
		return nil
	}
	_ = compressed
	var ix Index
	if err := json.Unmarshal(data, &ix); err != nil {
		return nil
	}
	if ix.Files == nil {
		ix.Files = map[string]int64{}
	}
	if ix.Sizes == nil {
		ix.Sizes = map[string]int64{}
	}
	return &ix
}

// LoadCached 载入磁盘缓存到内存（启动时调用，不扫描）。
func LoadCached(root string) *Index {
	ix, st := loadCacheFile(cachePath(), root)
	noteLoad(st.Hit, st.Ms, st.Symbols, st.Bytes)
	if ix == nil {
		return nil
	}
	setCurrent(ix)
	return ix
}

// Search 在索引里查找符号：完全匹配 > 前缀 > 单词边界 > 子串，其次按路径深度与名字长度。
func (ix *Index) Search(query string, limit int) []Symbol {
	q := strings.ToLower(strings.TrimSpace(query))
	if ix == nil || q == "" {
		return nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	type hit struct {
		s Symbol
		n int
	}
	var hits []hit
	for _, s := range ix.Syms {
		name := strings.ToLower(s.Name)
		score := 0
		switch {
		case name == q:
			score = 100
		case strings.HasPrefix(name, q):
			score = 80
		case boundaryMatch(s.Name, q):
			score = 65
		case strings.Contains(name, q):
			score = 40
		case strings.Contains(strings.ToLower(s.Sig), q):
			score = 20
		default:
			continue
		}
		// 路径更浅、名字更短的排前面
		score -= strings.Count(s.File, "/") * 2
		hits = append(hits, hit{s: s, n: score})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].n != hits[j].n {
			return hits[i].n > hits[j].n
		}
		if len(hits[i].s.Name) != len(hits[j].s.Name) {
			return len(hits[i].s.Name) < len(hits[j].s.Name)
		}
		if hits[i].s.File != hits[j].s.File {
			return hits[i].s.File < hits[j].s.File
		}
		return hits[i].s.Line < hits[j].s.Line
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Symbol, len(hits))
	for i, h := range hits {
		out[i] = h.s
	}
	return out
}

// boundaryMatch 判断 q 是否落在驼峰/下划线边界上（如 "turn" 命中 RunTurn）。
// q 需为小写，name 用原始大小写以识别驼峰边界。
func boundaryMatch(name, q string) bool {
	if len(q) == 0 || len(name) < len(q) {
		return false
	}
	low := strings.ToLower(name)
	for i := 0; i+len(q) <= len(low); i++ {
		if low[i:i+len(q)] != q {
			continue
		}
		if i == 0 {
			return true
		}
		prev := name[i-1]
		cur := name[i]
		if prev == '_' || prev == '/' || (prev >= 'a' && prev <= 'z' && cur >= 'A' && cur <= 'Z') {
			return true
		}
	}
	return false
}

// Stats 返回索引概要，供工具回显。
func (ix *Index) Stats() (files int, syms int) {
	if ix == nil {
		return 0, 0
	}
	return len(ix.Files), len(ix.Syms)
}
