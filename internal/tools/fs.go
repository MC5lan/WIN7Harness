package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"win7harness/internal/core"
)

func init() {
	register(Tool{
		Name: "read_file",
		Desc: "读取文本文件内容。返回带行号的文本，便于后续精确编辑。大文件请用 offset/limit 分页读取。",
		Schema: `{
  "type":"object",
  "properties":{
    "path":{"type":"string","description":"文件路径，相对工作目录或绝对路径"},
    "offset":{"type":"integer","description":"起始行号（从 1 开始），默认 1"},
    "limit":{"type":"integer","description":"最多读取行数，默认 800"}
  },
  "required":["path"]
}`,
		Run: runReadFile,
	})

	register(Tool{
		Name: "write_file",
		Desc: "创建或完整覆盖写入一个文本文件（自动创建父目录）。修改已有文件请优先用 edit_file，避免覆盖他人改动。",
		Schema: `{
  "type":"object",
  "properties":{
    "path":{"type":"string","description":"文件路径"},
    "content":{"type":"string","description":"完整文件内容"}
  },
  "required":["path","content"]
}`,
		Run: runWriteFile,
	})

	register(Tool{
		Name: "edit_file",
		Desc: "在文件中精确替换一段文本（old_string 必须在文件中唯一出现）。这是修改已有文件的首选方式。",
		Schema: `{
  "type":"object",
  "properties":{
    "path":{"type":"string","description":"文件路径"},
    "old_string":{"type":"string","description":"要被替换的原文（需唯一）"},
    "new_string":{"type":"string","description":"替换后的新内容，传空字符串表示删除"}
  },
  "required":["path","old_string","new_string"]
}`,
		Run: runEditFile,
	})

	register(Tool{
		Name: "list_dir",
		Desc: "列出目录内容（含子目录一层）。返回名称、类型与大小。",
		Schema: `{
  "type":"object",
  "properties":{
    "path":{"type":"string","description":"目录路径，默认工作目录"},
    "depth":{"type":"integer","description":"递归深度，默认 1，最大 3"}
  }
}`,
		Run: runListDir,
	})

	register(Tool{
		Name: "glob",
		Desc: "按通配符查找文件，如 **/*.go、src/**/*.tsx。返回匹配的文件路径列表。",
		Schema: `{
  "type":"object",
  "properties":{
    "pattern":{"type":"string","description":"通配符模式，支持 * 与 **"},
    "path":{"type":"string","description":"搜索根目录，默认工作目录"}
  },
  "required":["pattern"]
}`,
		Run: runGlob,
	})

	register(Tool{
		Name: "grep",
		Desc: "在目录中按正则搜索文件内容，返回 文件:行号:内容。用于快速定位代码。",
		Schema: `{
  "type":"object",
  "properties":{
    "pattern":{"type":"string","description":"正则表达式"},
    "path":{"type":"string","description":"搜索根目录，默认工作目录"},
    "include":{"type":"string","description":"只搜索匹配的文件名通配符，如 *.go，默认全部文本文件"},
    "max_results":{"type":"integer","description":"最多返回条数，默认 80"}
  },
  "required":["pattern"]
}`,
		Run: runGrep,
	})

	register(Tool{
		Name: "delete_path",
		Desc: "删除文件或空目录。删除前会请求用户确认，属于危险操作。",
		Schema: `{
  "type":"object",
  "properties":{
    "path":{"type":"string","description":"要删除的路径"}
  },
  "required":["path"]
}`,
		Run: runDelete,
	})

	register(Tool{
		Name: "todo_write",
		Desc: "维护当前任务清单（规划/进度）。传入完整清单会整体替换旧清单，用于让用户看到你的执行计划。",
		Schema: `{
  "type":"object",
  "properties":{
    "items":{
      "type":"array",
      "description":"任务清单，按顺序",
      "items":{
        "type":"object",
        "properties":{
          "text":{"type":"string"},
          "status":{"type":"string","enum":["pending","doing","done"]}
        },
        "required":["text","status"]
      }
    }
  },
  "required":["items"]
}`,
		Run: runTodoWrite,
	})
}

func runReadFile(ctx *Context, args map[string]interface{}) (Result, error) {
	p := core.ResolvePath(ctx.WorkDir, Get(args, "path"))
	if err := core.ValidateWindowsPathSyntax(p); err != nil {
		return Result{}, err
	}
	if err := guardRead(ctx, p, "读取"); err != nil {
		return Result{}, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return Result{}, fmt.Errorf("文件不存在: %s", p)
	}
	if st.IsDir() {
		return Result{}, fmt.Errorf("%s 是目录，请用 list_dir", p)
	}
	if st.Size() > 8<<20 {
		return Result{}, fmt.Errorf("文件过大（%s），请用 offset/limit 分段读取", core.HumanSize(st.Size()))
	}
	text, meta, err := readTextFile(p)
	if err != nil {
		if errors.Is(err, errBinaryText) {
			return Result{}, fmt.Errorf("%s 看起来是二进制文件，read_file 只读取文本；如需查看请用系统工具打开", filepath.Base(p))
		}
		return Result{}, err
	}
	// 展示层统一用 LF：模型不需要看到 \r，edit_file 的 old_string 也就能直接用 \n。
	text = normalizeForMatch(text)
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	offset := GetInt(args, "offset", 1)
	if offset < 1 {
		offset = 1
	}
	limit := GetInt(args, "limit", 800)
	if limit <= 0 {
		limit = 800
	}
	if limit > 4000 {
		limit = 4000
	}

	var b strings.Builder
	total := len(lines)
	printed := 0
	for i, line := range lines {
		n := i + 1
		if n < offset {
			continue
		}
		if printed >= limit {
			continue
		}
		fmt.Fprintf(&b, "%5d  %s\n", n, line)
		printed++
	}
	if total == 0 {
		return Result{Output: "（空文件）"}, nil
	}
	var header string
	if printed == 0 {
		header = fmt.Sprintf("%s（%s；共 %d 行，offset=%d 超出范围，未显示任何内容）\n", filepath.Base(p), meta.label(), total, offset)
	} else {
		header = fmt.Sprintf("%s（%s；共 %d 行，显示第 %d-%d 行）\n", filepath.Base(p), meta.label(), total, offset, offset+printed-1)
	}
	out := header + b.String()
	if offset+printed <= total {
		out += fmt.Sprintf("\n... 还有 %d 行未显示，可用 offset=%d 继续读取\n", total-(offset+printed-1), offset+printed)
	}
	return Result{Output: out}, nil
}

// guardEscapes 在路径越出工作区时请求用户确认。
// 没有确认回调（如子代理、无人值守）就拒绝，避免 agent 用 ../ 静默改到工作区之外。
func guardEscapes(ctx *Context, p, action string) error {
	if !core.EscapesWorkDir(ctx.WorkDir, p) {
		return nil
	}
	if ctx.Confirm == nil {
		return fmt.Errorf("%s 指向工作区之外，需用户确认：%s", action, p)
	}
	if !ctx.Confirm("工作区之外的路径", fmt.Sprintf("%s 指向工作区之外：\n%s\n是否继续？", action, p)) {
		return fmt.Errorf("用户拒绝%s工作区之外的 %s", action, p)
	}
	return nil
}

// guardRead 是守住读路径的越界防线（与写路径对称）。
// 读外部路径在交互场景下属于正常需求（用户自己说“读一下 C:\x”），
// 所以能弹确认就弹；无回调（子代理/无人值守）时拒绝——
// “模型被网页注入后读 ~/.ssh/config 再发出去”的链路由此截断。
func guardRead(ctx *Context, p, action string) error {
	if !core.EscapesWorkDir(ctx.WorkDir, p) {
		return nil
	}
	if ctx.Confirm == nil {
		return fmt.Errorf("%s 指向工作区之外，需用户确认：%s", action, p)
	}
	if !ctx.Confirm("读取工作区外的路径", fmt.Sprintf("%s 指向工作区之外：\n%s\n是否继续？", action, p)) {
		return fmt.Errorf("用户拒绝%s工作区之外的 %s", action, p)
	}
	return nil
}

func runWriteFile(ctx *Context, args map[string]interface{}) (Result, error) {
	p := core.ResolvePath(ctx.WorkDir, Get(args, "path"))
	if err := core.ValidateWindowsPathSyntax(p); err != nil {
		return Result{}, err
	}
	content := Get(args, "content")
	if other, ok := core.CaseInsensitiveCollision(p); ok {
		return Result{}, fmt.Errorf("目标与已有文件仅大小写不同：%s 已存在；请改用原大小写，避免 Windows 上覆盖", other)
	}
	if core.IsForbiddenPath(p) {
		return Result{}, fmt.Errorf("拒绝写入系统保护目录: %s", p)
	}
	if err := guardEscapes(ctx, p, "写入"); err != nil {
		return Result{}, err
	}
	existed := false
	meta := defaultTextMeta(p)
	if st, err := os.Stat(p); err == nil {
		existed = true
		if st.IsDir() {
			return Result{}, fmt.Errorf("%s 是目录", p)
		}
		if st.Size() > 4<<20 && len(content) < int(st.Size())/4 {
			// 大文件被极小内容覆盖，视为危险操作
			if ctx.Confirm == nil {
				return Result{}, fmt.Errorf("覆盖大文件需要用户确认，但当前没有确认通道（子代理/无人值守）：%s", p)
			}
			if !ctx.Confirm("覆盖大文件", fmt.Sprintf("%s 现有 %s，新内容仅 %s，是否覆盖？",
				filepath.Base(p), core.HumanSize(st.Size()), core.HumanSize(int64(len(content))))) {
				return Result{}, fmt.Errorf("用户拒绝覆盖 %s", p)
			}
		}
		if oldData, rerr := os.ReadFile(p); rerr == nil {
			oldText, oldMeta, derr := decodeTextBytes(oldData)
			if derr == nil {
				// 已有文件保留原编码/EOL；覆盖写入不能把 GBK 老项目悄悄改成 UTF-8。
				meta = oldMeta
				if !confirmDiff(ctx, p, normalizeForMatch(oldText), normalizeForMatch(content), "覆盖写入") {
					return Result{}, fmt.Errorf("用户取消了写入: %s", p)
				}
			} else if !confirmDiff(ctx, p, string(oldData), content, "覆盖写入") {
				return Result{}, fmt.Errorf("用户取消了写入: %s", p)
			}
		}
	}
	// 新建 .txt/.md/.log 时 EOL 留空，表示跟随写入内容：
	// 模型给 LF 就写 LF，给 CRLF 就写 CRLF，不强行改成 Windows 风格。
	if meta.EOL == "" {
		meta.EOL = detectEOL(normalizeForMatch(content))
	}
	// 先编码再快照：如果新内容无法用原编码表示（例如往 GBK 文件里塞 emoji），
	// 应该在动文件之前就失败，而不是留下一条无用的快照。
	data, err := encodeText(content, meta)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return Result{}, err
	}
	// 写前快照失败不写：快照是唯一的后悔药，吞掉它再写等于裸奔。
	if err := core.SnapshotFile(p); err != nil {
		return Result{}, fmt.Errorf("写前快照失败：%v——已中止（未写文件）", err)
	}
	if err := core.WriteFileAtomic(p, data, 0o644); err != nil {
		// 写失败：原子写保证原文件没被动过，把这条快照撤掉，别污染 /undo 栈
		core.DiscardLastSnapshot()
		return Result{}, fmt.Errorf("写入失败（原文件未改动）：%v", err)
	}
	verb := "创建"
	if existed {
		verb = "更新"
	}
	lines := strings.Count(normalizeForMatch(content), "\n") + 1
	return Result{
		Output: fmt.Sprintf("已%s %s（%d 字节，%d 行，%s）", verb, p, len(data), lines, meta.label()),
	}, nil
}

func runEditFile(ctx *Context, args map[string]interface{}) (Result, error) {
	p := core.ResolvePath(ctx.WorkDir, Get(args, "path"))
	if err := core.ValidateWindowsPathSyntax(p); err != nil {
		return Result{}, err
	}
	oldStr := Get(args, "old_string")
	newStr := Get(args, "new_string")
	if oldStr == "" {
		return Result{}, fmt.Errorf("old_string 不能为空")
	}
	if core.IsForbiddenPath(p) {
		return Result{}, fmt.Errorf("拒绝修改系统保护目录: %s", p)
	}
	if err := guardEscapes(ctx, p, "修改"); err != nil {
		return Result{}, err
	}
	text, meta, err := readTextFile(p)
	if err != nil {
		if errors.Is(err, errBinaryText) {
			return Result{}, fmt.Errorf("%s 是二进制文件，edit_file 只支持文本", filepath.Base(p))
		}
		return Result{}, fmt.Errorf("读取失败: %v", err)
	}
	// 统一按 LF 匹配：GBK 文件能读，CRLF 文件也不用模型在 old_string 里带 \r。
	normText := normalizeForMatch(text)
	oldNorm := normalizeForMatch(oldStr)
	newNorm := normalizeForMatch(newStr)
	if oldNorm == "" {
		return Result{}, fmt.Errorf("old_string 归一化后为空")
	}
	n := strings.Count(normText, oldNorm)
	if n == 0 {
		return Result{}, fmt.Errorf("未在 %s 中找到待替换内容（文件编码 %s，换行 %s），请先用 read_file 确认原文",
			filepath.Base(p), meta.Enc, meta.label())
	}
	if n > 1 {
		return Result{}, fmt.Errorf("待替换内容出现了 %d 次，不唯一。请提供更多上下文使其唯一", n)
	}
	updated := strings.Replace(normText, oldNorm, newNorm, 1)
	if !confirmDiff(ctx, p, normText, updated, "修改") {
		return Result{}, fmt.Errorf("用户取消了修改: %s", p)
	}
	data, err := encodeText(updated, meta)
	if err != nil {
		return Result{}, err
	}
	// 写前快照失败不写：快照是唯一的后悔药。
	if err := core.SnapshotFile(p); err != nil {
		return Result{}, fmt.Errorf("写前快照失败：%v——已中止（未改文件）", err)
	}
	if err := core.WriteFileAtomic(p, data, 0o644); err != nil {
		core.DiscardLastSnapshot()
		return Result{}, fmt.Errorf("写入失败（原文件未改动）：%v", err)
	}
	return Result{Output: fmt.Sprintf("已修改 %s（%d 字节 → %d 字节，保留 %s）", p, len(text), len(updated), meta.label())}, nil
}

func runListDir(ctx *Context, args map[string]interface{}) (Result, error) {
	p := core.ResolvePath(ctx.WorkDir, Get(args, "path"))
	if err := core.ValidateWindowsPathSyntax(p); err != nil {
		return Result{}, err
	}
	// list_dir 本身有 depth<=3 与 400 项上限，列一层用户目录是安全且有意义的，
	// 不做 guardSearchRoot 的“根目录拒绝”——那是给 glob/grep 递归扫描用的。
	if err := guardRead(ctx, p, "列目录"); err != nil {
		return Result{}, err
	}
	depth := GetInt(args, "depth", 1)
	if depth < 1 {
		depth = 1
	}
	if depth > 3 {
		depth = 3
	}
	var b strings.Builder
	count := 0
	var totalDirs int
	var walk func(dir string, d int, prefix string)
	walk = func(dir string, d int, prefix string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			fmt.Fprintf(&b, "%s[无法读取: %v]\n", prefix, err)
			return
		}
		totalDirs += len(entries)
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].IsDir() != entries[j].IsDir() {
				return entries[i].IsDir()
			}
			return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
		})
		for _, e := range entries {
			if count > 400 {
				return
			}
			count++
			info, _ := e.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			mark := ""
			if e.IsDir() {
				mark = "/"
			}
			line := prefix + e.Name() + mark
			if !e.IsDir() {
				line += "  " + core.HumanSize(size)
			}
			b.WriteString(line + "\n")
			if e.IsDir() && d < depth && !strings.HasPrefix(e.Name(), ".") && e.Name() != "node_modules" {
				walk(filepath.Join(dir, e.Name()), d+1, prefix+"  ")
			}
		}
	}
	walk(p, 1, "")
	if count == 0 {
		return Result{Output: "（空目录）"}, nil
	}
	note := ""
	if totalDirs > 400 {
		note = fmt.Sprintf("\n…（仅前 400 项，共约 %d 项；用更深的子路径缩小范围）", totalDirs)
	}
	return Result{Output: fmt.Sprintf("%s\n%s%s", p, b.String(), note)}, nil
}

// 递归检索的硬上限：老机器/杀软环境下，扫 C:\Users 这种目录几分钟都跑不完。
// 宁可给模型一句“缩小范围”，也不要让用户以为程序卡死。
const (
	walkMaxEntries  = 20000
	walkMaxDuration = 8 * time.Second
)

var errWalkBudget = errors.New("walk budget reached")

// walkSkipDirs 是递归检索时跳过的目录名（小写比较）。
// 系统目录与依赖目录里的内容不是项目代码，扫它们只会烧时间和内存。
var walkSkipDirs = map[string]bool{
	".git": true, ".svn": true, ".hg": true,
	"node_modules": true, "vendor": true, "__pycache__": true, ".venv": true,
	"dist": true, "$recycle.bin": true, "system volume information": true,
	"appdata": true, "windows": true, "program files": true,
	"program files (x86)": true, "programdata": true,
}

// guardSearchRoot 拒绝在盘符根、C:\Users、Windows、ProgramData 等目录做递归搜索。
// 这些目录动辄几十万文件，既费时间又费 token；让模型直接把 path 指到项目目录。
func guardSearchRoot(root string) error {
	clean := filepath.Clean(root)
	vol := filepath.VolumeName(clean)
	if vol != "" && strings.EqualFold(clean, filepath.Clean(vol+`\`)) {
		return fmt.Errorf("拒绝在盘符根目录 %s 做递归检索：请把 path 指到具体项目目录", root)
	}
	low := strings.ToLower(clean)
	for _, p := range []string{`c:\users`, `c:\windows`, `c:\program files`, `c:\program files (x86)`, `c:\programdata`} {
		if low == p {
			return fmt.Errorf("拒绝在系统/用户根目录 %s 做递归检索：会遍历数万文件并卡住界面。请把 path 指到具体项目目录，或改用 skill_list / 已知路径", root)
		}
	}
	for _, env := range []string{os.Getenv("USERPROFILE"), os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA")} {
		if env != "" && strings.EqualFold(clean, filepath.Clean(env)) {
			return fmt.Errorf("拒绝在用户数据根目录 %s 做递归检索：请缩小到具体项目/技能目录", root)
		}
	}
	return nil
}

func skipWalkDir(name string, path, root string) bool {
	return path != root && walkSkipDirs[strings.ToLower(name)]
}

func runGlob(ctx *Context, args map[string]interface{}) (Result, error) {
	pattern := Get(args, "pattern")
	root := core.ResolvePath(ctx.WorkDir, Get(args, "path"))
	if err := core.ValidateWindowsPathSyntax(root); err != nil {
		return Result{}, err
	}
	if root == "" {
		root = ctx.WorkDir
	}
	if err := guardSearchRoot(root); err != nil {
		return Result{}, err
	}
	if err := guardRead(ctx, root, "检索"); err != nil {
		return Result{}, err
	}
	var matches []string
	hasSep := strings.Contains(pattern, "/")
	var re *regexp.Regexp
	if hasSep {
		re = regexp.MustCompile(globToRegex(pattern))
	}
	entries := 0
	truncated := false
	deadline := time.Now().Add(walkMaxDuration)
	_ = filepath.Walk(root, func(path string, info fs.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if ctx.Ctx != nil && ctx.Ctx.Err() != nil {
			truncated = true
			return errWalkBudget
		}
		entries++
		if entries > walkMaxEntries || time.Now().After(deadline) {
			truncated = true
			return errWalkBudget
		}
		if info.IsDir() {
			if skipWalkDir(info.Name(), path, root) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		ok := false
		if re != nil {
			ok = re.MatchString(rel)
		} else {
			ok, _ = filepath.Match(pattern, info.Name())
		}
		if ok {
			matches = append(matches, rel)
			if len(matches) >= 300 {
				truncated = true
				return errWalkBudget
			}
		}
		return nil
	})
	sort.Strings(matches)
	note := ""
	if truncated {
		note = fmt.Sprintf("\n…扫描 %d 项后停止（上限 %d 项 / %s）；请把 path 缩小到具体目录，或用更精确的 pattern。",
			entries, walkMaxEntries, walkMaxDuration)
	}
	if len(matches) == 0 {
		return Result{Output: fmt.Sprintf("未找到匹配 %s 的文件（扫描 %d 项）%s", pattern, entries, note)}, nil
	}
	return Result{Output: fmt.Sprintf("匹配 %d 个文件（扫描 %d 项）：\n%s%s", len(matches), entries, strings.Join(matches, "\n"), note)}, nil
}

func globToRegex(glob string) string {
	var b strings.Builder
	b.WriteString("^")
	g := strings.ReplaceAll(glob, "\\", "/")
	for i := 0; i < len(g); i++ {
		c := g[i]
		switch c {
		case '*':
			if i+1 < len(g) && g[i+1] == '*' {
				b.WriteString(".*")
				i++
				if i+1 < len(g) && g[i+1] == '/' {
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.':
			b.WriteString("\\.")
		default:
			if strings.ContainsRune(`+()[]{}^$|`, rune(c)) {
				b.WriteByte('\\')
			}
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	return b.String()
}

func runGrep(ctx *Context, args map[string]interface{}) (Result, error) {
	pattern := Get(args, "pattern")
	root := core.ResolvePath(ctx.WorkDir, Get(args, "path"))
	if err := core.ValidateWindowsPathSyntax(root); err != nil {
		return Result{}, err
	}
	if err := guardSearchRoot(root); err != nil {
		return Result{}, err
	}
	if err := guardRead(ctx, root, "检索"); err != nil {
		return Result{}, err
	}
	include := Get(args, "include")
	maxResults := GetInt(args, "max_results", 80)
	if maxResults <= 0 {
		maxResults = 80
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Result{}, fmt.Errorf("正则非法: %v", err)
	}
	var out []string
	fileCount := 0
	entries := 0
	totalBytes := int64(0)
	truncated := false
	deadline := time.Now().Add(walkMaxDuration)
	_ = filepath.Walk(root, func(path string, info fs.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		if ctx.Ctx != nil && ctx.Ctx.Err() != nil {
			truncated = true
			return errWalkBudget
		}
		entries++
		if entries > walkMaxEntries || time.Now().After(deadline) || fileCount >= 5000 {
			truncated = true
			return errWalkBudget
		}
		if info.IsDir() {
			if skipWalkDir(info.Name(), path, root) {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Size() > 2<<20 {
			return nil
		}
		if include != "" {
			if ok, _ := filepath.Match(include, info.Name()); !ok {
				return nil
			}
		}
		if !isTextFile(info.Name()) {
			return nil
		}
		fileCount++
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		totalBytes += int64(len(data))
		if totalBytes > 32<<20 {
			truncated = true
			return errWalkBudget
		}
		rel, _ := filepath.Rel(root, path)
		lines := strings.Split(string(data), "\n")
		for i, ln := range lines {
			if re.MatchString(ln) {
				trim := strings.TrimSpace(ln)
				if len(trim) > 240 {
					trim = trim[:240] + "…"
				}
				out = append(out, fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), i+1, trim))
				if len(out) >= maxResults {
					truncated = true
					return errWalkBudget
				}
			}
		}
		return nil
	})
	note := ""
	if truncated {
		note = fmt.Sprintf("\n…扫描 %d 项 / %d 个文件后停止；请缩小 path 或加 include 过滤。", entries, fileCount)
	}
	if len(out) == 0 {
		return Result{Output: fmt.Sprintf("在 %d 个文件中未匹配到 %s%s", fileCount, pattern, note)}, nil
	}
	return Result{Output: strings.Join(out, "\n") + note}, nil
}

func isTextFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".c", ".h", ".cpp", ".hpp", ".cs",
		".java", ".kt", ".rs", ".rb", ".php", ".swift", ".m", ".mm", ".sh", ".bat", ".cmd",
		".ps1", ".sql", ".json", ".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf", ".md",
		".txt", ".html", ".htm", ".css", ".scss", ".less", ".xml", ".vue", ".svelte",
		".gitignore", ".env", ".mod", ".sum", ".gradle", ".properties", ".log", ".csv":
		return true
	}
	return ext == ""
}

func runDelete(ctx *Context, args map[string]interface{}) (Result, error) {
	p := core.ResolvePath(ctx.WorkDir, Get(args, "path"))
	if err := core.ValidateWindowsPathSyntax(p); err != nil {
		return Result{}, err
	}
	if other, ok := core.CaseInsensitiveCollision(p); ok {
		return Result{}, fmt.Errorf("目标与已有文件仅大小写不同：%s 已存在；请改用原大小写，避免误删", other)
	}
	if core.IsForbiddenPath(p) {
		return Result{}, fmt.Errorf("拒绝删除系统保护目录: %s", p)
	}
	if err := guardEscapes(ctx, p, "删除"); err != nil {
		return Result{}, err
	}
	if ctx.Confirm == nil {
		// fail-closed：没有确认通道就不删——删除不可逆（有快照，但语义不同，
		// 不能靠 /undo 兜底用户没授权的删除）。
		return Result{}, fmt.Errorf("删除文件需要用户确认，但当前没有确认通道（子代理/无人值守）：%s", p)
	}
	if ctx.Confirm != nil && !ctx.Confirm("删除文件", "即将删除：\n"+p) {
		return Result{}, fmt.Errorf("用户取消了删除操作")
	}
	st, err := os.Stat(p)
	if err != nil {
		return Result{}, err
	}
	if !st.IsDir() {
		// 删除前快照失败不删：删了就没了，快照是唯一的后悔药。
		if err := core.SnapshotFile(p); err != nil {
			return Result{}, fmt.Errorf("删除前快照失败：%v——已中止", err)
		}
		if err := os.Remove(p); err != nil {
			return Result{}, err
		}
	} else {
		entries, err := os.ReadDir(p)
		if err != nil {
			return Result{}, fmt.Errorf("读取目录失败：%v", err)
		}
		if len(entries) > 0 {
			return Result{}, fmt.Errorf("目录非空，出于安全考虑不递归删除: %s", p)
		}
		if err := os.Remove(p); err != nil {
			return Result{}, err
		}
	}
	return Result{Output: "已删除 " + p}, nil
}

func runTodoWrite(ctx *Context, args map[string]interface{}) (Result, error) {
	raw, _ := args["items"].([]interface{})
	var b strings.Builder
	b.WriteString("任务清单已更新：\n")
	for i, it := range raw {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		mark := "○"
		switch Get(m, "status") {
		case "done":
			mark = "✓"
		case "doing":
			mark = "◐"
		}
		fmt.Fprintf(&b, "%s %d. %s\n", mark, i+1, Get(m, "text"))
	}
	// 同时把清单渲染成 markdown，便于在对话流中展示
	var md strings.Builder
	md.WriteString("**执行计划**\n\n")
	for _, it := range raw {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		st := "pending"
		if v, ok := m["status"].(string); ok {
			st = v
		}
		box := "- [ ] "
		if st == "done" {
			box = "- [x] "
		}
		md.WriteString(box + Get(m, "text") + "\n")
	}
	b.WriteString("\n")
	b.WriteString(md.String())
	return Result{Output: b.String(), Display: true, Title: "执行计划"}, nil
}

// confirmDiff 按配置决定是否在写盘前弹出统一差异预览（off/large/always）。
// 返回 true 表示允许继续写入。
func confirmDiff(ctx *Context, p, oldText, newText, verb string) bool {
	if ctx.Confirm == nil && ctx.ConfirmDiff == nil {
		return true
	}
	mode := core.Get().DiffPreview
	if mode == "off" || mode == "" {
		return true
	}
	if !utf8.ValidString(oldText) || !utf8.ValidString(newText) {
		return true // 二进制内容不做行级 diff，交给原有的大文件/越界确认
	}
	added, removed := core.DiffStat(oldText, newText)
	if added == 0 && removed == 0 {
		return true
	}
	if mode == "large" {
		total := added + removed
		if total < 40 {
			return true // 小改动直接放行，避免打断
		}
	}
	title := verb + "前确认（差异预览）"
	if ctx.ConfirmDiff != nil {
		hunks := core.DiffHunks(oldText, newText, filepath.Base(p), 3)
		// 整文件写入无法只落地某几个 hunk，所以界面不提供逐段选择。
		accepted := ctx.ConfirmDiff(title, filepath.Base(p), hunks, false)
		return accepted != nil && len(accepted) == len(hunks)
	}
	if ctx.Confirm == nil {
		return true
	}
	body := core.UnifiedDiff(oldText, newText, filepath.Base(p), 3)
	return ctx.Confirm(title, body)
}

// confirmDiff 按配置决定是否在写盘前弹出统一差异预览（off/large/always）。
// 返回 true 表示允许继续写入。
