package codeidx

// 调用关系检索：解析 Go 源码建立"函数 → 被调用者 / 调用者"双向图。
// 与符号索引互补：符号索引回答"在哪定义"，调用图回答"谁在用、用了谁"。

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// CallSite 一处调用点。
type CallSite struct {
	File string // 相对路径
	Line int
	From string // 调用方函数名
}

// CallGraph 调用关系图。
type CallGraph struct {
	Root string
	// callers[被调用者] = 调用点列表
	callers map[string][]CallSite
	// callees[调用方] = 被调用者集合
	callees map[string]map[string]bool
	// declFile[函数名] = 定义文件:行
	declFile map[string]CallSite
	Files    int
	Funcs    int
}

var (
	graphMu   sync.Mutex
	graphRoot string
	graphVal  *CallGraph

	graphFileMu sync.Mutex
	graphFiles  = map[string]*fileGraph{}
	graphParsed int64
)

// fileGraph 是单个 .go 文件解析出的调用贡献，用于跨 BuildGraph 复用。
type fileGraph struct {
	rel     string
	mtime   int64
	size    int64
	funcs   int
	decl    map[string]CallSite
	callees map[string]map[string]bool
	callers map[string][]CallSite
}

// Graph 返回 root 的调用图（带缓存）。文件变动后由 ResetGraph / MarkDirtyAbs 失效。
// 构建过程不长时间持有 graphMu，避免另一个 root 的查询被整图构建阻塞。
func Graph(root string) *CallGraph {
	graphMu.Lock()
	if graphVal != nil && graphRoot == root {
		g := graphVal
		graphMu.Unlock()
		return g
	}
	graphMu.Unlock()

	g := BuildGraph(root)

	graphMu.Lock()
	if graphVal == nil || graphRoot != root {
		graphVal = g
		graphRoot = root
	}
	out := graphVal
	graphMu.Unlock()
	return out
}

// ResetGraph 清空调用图缓存（文件级解析缓存保留，MarkDirtyAbs 负责精确失效）。
func ResetGraph() {
	graphMu.Lock()
	graphVal = nil
	graphRoot = ""
	graphMu.Unlock()
}

// invalidateGraphFile 让某个文件下一次 BuildGraph 重新解析。
func invalidateGraphFile(abs string) {
	if abs == "" {
		return
	}
	graphFileMu.Lock()
	delete(graphFiles, normKey(abs))
	graphFileMu.Unlock()
}

// skipCallDir 跳过的目录，与符号索引保持一致。
func skipCallDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", "testdata", ".idea", ".vscode":
		return true
	}
	return strings.HasPrefix(name, ".")
}

// errStopGraphWalk 用于 filepath.Walk 达到文件上限时提前退出。
var errStopGraphWalk = errors.New("call graph file limit reached")

// BuildGraph 遍历 root 下所有 .go 文件建立调用图。
// 每个文件的 AST 结果按 mtime/size 缓存，编辑一个文件后只重解析该文件。
func BuildGraph(root string) *CallGraph {
	g := &CallGraph{
		Root:     root,
		callers:  map[string][]CallSite{},
		callees:  map[string]map[string]bool{},
		declFile: map[string]CallSite{},
	}
	seen := map[string]bool{}
	count := 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if skipCallDir(info.Name()) && filepath.Clean(path) != filepath.Clean(root) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".go") {
			return nil
		}
		if info.Size() > maxFileSz {
			return nil
		}
		if count >= maxFiles {
			return errStopGraphWalk
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		if fd := cachedFileGraph(path, rel, info); fd != nil {
			g.merge(fd)
			seen[normKey(path)] = true
			count++
		}
		return nil
	})
	// 清理已删除文件的解析缓存，避免切换仓库/删文件后无限增长。
	graphFileMu.Lock()
	for key := range graphFiles {
		if !seen[key] {
			delete(graphFiles, key)
		}
	}
	graphFileMu.Unlock()
	return g
}

func cachedFileGraph(abs, rel string, info os.FileInfo) *fileGraph {
	key := normKey(abs)
	mtime := info.ModTime().UnixNano()
	graphFileMu.Lock()
	if fd, ok := graphFiles[key]; ok && fd.rel == rel && fd.mtime == mtime && fd.size == info.Size() {
		graphFileMu.Unlock()
		return fd
	}
	graphFileMu.Unlock()

	fd := parseFileData(abs, rel, mtime, info.Size())
	if fd == nil {
		return nil
	}
	graphFileMu.Lock()
	graphFiles[key] = fd
	graphFileMu.Unlock()
	return fd
}

func (g *CallGraph) merge(fd *fileGraph) {
	if fd == nil {
		return
	}
	g.Files++
	g.Funcs += fd.funcs
	for name, site := range fd.decl {
		g.declFile[name] = site
	}
	for from, set := range fd.callees {
		if g.callees[from] == nil {
			g.callees[from] = map[string]bool{}
		}
		for callee := range set {
			g.callees[from][callee] = true
		}
	}
	for callee, sites := range fd.callers {
		g.callers[callee] = append(g.callers[callee], sites...)
	}
}

func parseFileData(abs, rel string, mtime, size int64) *fileGraph {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, nil, 0)
	if err != nil || f == nil {
		return nil
	}
	atomic.AddInt64(&graphParsed, 1)
	fd := &fileGraph{
		rel:     rel,
		mtime:   mtime,
		size:    size,
		decl:    map[string]CallSite{},
		callees: map[string]map[string]bool{},
		callers: map[string][]CallSite{},
	}
	pos := func(p token.Pos) int { return fset.Position(p).Line }
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			if r := recvName(fn.Recv.List[0].Type); r != "" {
				name = r + "." + name
			}
		}
		fd.funcs++
		fd.decl[name] = CallSite{File: rel, Line: pos(fn.Pos()), From: name}
		if fd.callees[name] == nil {
			fd.callees[name] = map[string]bool{}
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee := callName(call.Fun)
			if callee == "" {
				return true
			}
			fd.callees[name][callee] = true
			fd.callers[callee] = append(fd.callers[callee], CallSite{File: rel, Line: pos(call.Pos()), From: name})
			return true
		})
	}
	return fd
}

// callName 从调用表达式里抽被调用名。
// 支持 f()、pkg.F()、x.M()、x.Y.M()；统一取最后一级名字。
func callName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.FuncLit:
		return ""
	case *ast.IndexExpr:
		return callName(x.X)
	case *ast.ParenExpr:
		return callName(x.X)
	}
	return ""
}

// Callers 返回调用 sym 的位置，最多 limit 条（0 表示默认 50）。
func (g *CallGraph) Callers(sym string, limit int) []CallSite {
	if limit <= 0 {
		limit = 50
	}
	var out []CallSite
	for _, s := range g.callers[sym] {
		out = append(out, s)
		if len(out) >= limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// Callees 返回 sym 直接调用的函数名（字典序）。
func (g *CallGraph) Callees(sym string) []string {
	m := g.callees[sym]
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DeclSite 返回函数定义位置。
func (g *CallGraph) DeclSite(sym string) (CallSite, bool) {
	s, ok := g.declFile[sym]
	return s, ok
}

// Resolve 把用户输入解析成图里的名字：支持 "pkg.Fn"、"Type.Method"、"Fn"。
func (g *CallGraph) Resolve(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if _, ok := g.declFile[name]; ok {
		return name
	}
	short := name
	if i := strings.LastIndexAny(name, "./"); i >= 0 {
		short = name[i+1:]
	}
	var hits []string
	for k := range g.declFile {
		if k == short || strings.HasSuffix(k, "."+short) {
			hits = append(hits, k)
		}
	}
	if len(hits) == 1 {
		return hits[0]
	}
	sort.Strings(hits)
	if len(hits) > 1 {
		return hits[0]
	}
	return name
}

// Ambiguous 返回全部同名候选（用于提示用户）。空表示唯一或不存在。
func (g *CallGraph) Ambiguous(name string) []string {
	short := name
	if i := strings.LastIndexAny(name, "./"); i >= 0 {
		short = name[i+1:]
	}
	var hits []string
	for k := range g.declFile {
		if k == name {
			return nil
		}
		if strings.HasSuffix(k, "."+short) || k == short {
			hits = append(hits, k)
		}
	}
	sort.Strings(hits)
	if len(hits) <= 1 {
		return nil
	}
	return hits
}
