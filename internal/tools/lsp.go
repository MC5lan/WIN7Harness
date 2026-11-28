package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/lsp"
)

const (
	lspMaxClients  = 4
	lspIdleTimeout = 10 * time.Minute
)

type lspEntry struct {
	client *lsp.Client
	last   time.Time
	busy   sync.Mutex
}

var (
	lspPoolMu sync.Mutex
	lspPool   = map[string]*lspEntry{}
)

func lspPoolKey(root, cmd string, args []string) string {
	return root + "\x00" + cmd + "\x00" + strings.Join(args, "\x00")
}

// getLSPClient 按 root + 命令复用语言服务器；同一 root 的请求串行执行。
func getLSPClient(ctx context.Context, cmd string, args []string, root string) (string, *lspEntry, error) {
	key := lspPoolKey(root, cmd, args)
	now := time.Now()

	lspPoolMu.Lock()
	defer lspPoolMu.Unlock()
	for k, e := range lspPool {
		if now.Sub(e.last) > lspIdleTimeout {
			delete(lspPool, k)
			go e.client.Close()
		}
	}
	if e, ok := lspPool[key]; ok {
		e.last = now
		return key, e, nil
	}
	if len(lspPool) >= lspMaxClients {
		var oldestKey string
		var oldest *lspEntry
		for k, e := range lspPool {
			if oldest == nil || e.last.Before(oldest.last) {
				oldestKey, oldest = k, e
			}
		}
		if oldest != nil {
			delete(lspPool, oldestKey)
			go oldest.client.Close()
		}
	}
	client, err := lsp.Start(ctx, cmd, args, root)
	if err != nil {
		return "", nil, err
	}
	e := &lspEntry{client: client, last: time.Now()}
	lspPool[key] = e
	return key, e, nil
}

func dropLSPClient(key string, e *lspEntry) {
	lspPoolMu.Lock()
	if cur, ok := lspPool[key]; ok && cur == e {
		delete(lspPool, key)
	}
	lspPoolMu.Unlock()
	go e.client.Close()
}

func touchLSPClient(e *lspEntry) {
	lspPoolMu.Lock()
	e.last = time.Now()
	lspPoolMu.Unlock()
}

// ShutdownLSP 关闭所有缓存的语言服务器；应用退出时调用。
func ShutdownLSP() {
	lspPoolMu.Lock()
	entries := lspPool
	lspPool = map[string]*lspEntry{}
	lspPoolMu.Unlock()
	for _, e := range entries {
		e.client.Close()
	}
}

func init() {
	register(Tool{
		Name: "lsp",
		Desc: "IDE 级语言服务：诊断、定义跳转、引用查找、悬停。自动使用 gopls（Go）/ pylsp（Python）/ typescript-language-server（TS/JS）/ rust-analyzer（Rust）；没装对应服务器时返回安装提示。",
		Schema: `{
  "type":"object",
  "properties":{
    "action":{"type":"string","enum":["diagnostics","definition","references","hover"],"description":"要执行的动作"},
    "path":{"type":"string","description":"目标文件路径（相对工作目录或绝对）"},
    "line":{"type":"integer","description":"行号，1-based（definition/references/hover 必填）"},
    "column":{"type":"integer","description":"列号，1-based（默认 1）"}
  },
  "required":["action","path"]
}`,
		Run: runLSP,
	})
}

func runLSP(ctx *Context, args map[string]interface{}) (Result, error) {
	action := strings.ToLower(strings.TrimSpace(Get(args, "action")))
	path := coreResolveDiagnostics(ctx, Get(args, "path"))
	if path == "" {
		return Result{}, fmt.Errorf("path 不能为空")
	}
	if err := core.ValidateWindowsPathSyntax(path); err != nil {
		return Result{}, err
	}
	if err := guardRead(ctx, path, "LSP"); err != nil {
		return Result{}, err
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return Result{}, fmt.Errorf("文件不存在：%s", path)
	}
	abs, _ := filepath.Abs(path)
	cmd, cmdArgs, ok := lspCommandFor(abs)
	if !ok {
		return Result{}, fmt.Errorf("没有找到 %s 对应的语言服务器。安装 gopls / pylsp / typescript-language-server / rust-analyzer 后可用；当前仍可用 diagnostics 工具做编译诊断", filepath.Ext(abs))
	}
	root := lspRoot(ctx.WorkDir, abs)
	baseCtx := context.Background()
	if ctx.Ctx != nil {
		baseCtx = ctx.Ctx
	}
	startCtx, cancel := context.WithTimeout(baseCtx, 30*time.Second)
	defer cancel()
	key, entry, err := getLSPClient(startCtx, cmd, cmdArgs, root)
	if err != nil {
		return Result{}, err
	}
	entry.busy.Lock()
	defer entry.busy.Unlock()
	defer touchLSPClient(entry)
	client := entry.client
	fail := func(err error) (Result, error) {
		dropLSPClient(key, entry)
		return Result{}, err
	}
	uri, err := client.DidOpen(abs)
	if err != nil {
		return fail(err)
	}
	line := GetInt(args, "line", 1)
	col := GetInt(args, "column", 1)
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	switch action {
	case "diagnostics", "":
		ds := client.WaitDiagnostics(baseCtx, uri, 15*time.Second)
		if len(ds) == 0 {
			return Result{Output: fmt.Sprintf("LSP 诊断：%s 没有发现问题。", filepath.Base(abs)), Display: true, Title: "LSP 诊断"}, nil
		}
		var out []string
		for _, d := range ds {
			out = append(out, fmt.Sprintf("%s:%d:%d: [%s] %s %s",
				filepath.Base(abs), d.Range.Start.Line+1, d.Range.Start.Character+1,
				severityName(d.Severity), d.Message, d.Source))
		}
		return Result{Output: "LSP 诊断：\n" + strings.Join(out, "\n"), Display: true, Title: fmt.Sprintf("LSP 诊断（%d）", len(ds))}, nil
	case "definition", "references":
		var locs []lsp.Location
		if action == "definition" {
			locs, err = client.Definition(baseCtx, uri, line-1, col-1)
		} else {
			locs, err = client.References(baseCtx, uri, line-1, col-1)
		}
		if err != nil {
			return fail(err)
		}
		if len(locs) == 0 {
			return Result{Output: fmt.Sprintf("LSP %s：没有结果。", action), Display: true, Title: "LSP"}, nil
		}
		var out []string
		for _, loc := range locs {
			p := lsp.URIToPath(loc.URI)
			out = append(out, fmt.Sprintf("%s:%d:%d", filepath.ToSlash(p), loc.Range.Start.Line+1, loc.Range.Start.Character+1))
		}
		sort.Strings(out)
		return Result{Output: "LSP " + action + "：\n" + strings.Join(out, "\n"), Display: true, Title: "LSP " + action}, nil
	case "hover":
		text, err := client.Hover(baseCtx, uri, line-1, col-1)
		if err != nil {
			return fail(err)
		}
		if strings.TrimSpace(text) == "" {
			return Result{Output: "LSP hover：没有结果。", Display: true, Title: "LSP hover"}, nil
		}
		return Result{Output: text, Display: true, Title: "LSP hover"}, nil
	default:
		return Result{}, fmt.Errorf("未知 action：%s（diagnostics/definition/references/hover）", action)
	}
}

func lspCommandFor(path string) (string, []string, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	candidates := []struct {
		cmd  string
		args []string
	}{
		{"gopls", nil},
	}
	switch ext {
	case ".go":
	case ".py":
		candidates = []struct {
			cmd  string
			args []string
		}{{"pylsp", nil}, {"python", []string{"-m", "pylsp"}}}
	case ".ts", ".tsx", ".js", ".jsx":
		candidates = []struct {
			cmd  string
			args []string
		}{{"typescript-language-server", []string{"--stdio"}}}
	case ".rs":
		candidates = []struct {
			cmd  string
			args []string
		}{{"rust-analyzer", nil}}
	default:
		return "", nil, false
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c.cmd); err == nil {
			return c.cmd, c.args, true
		}
	}
	return "", nil, false
}

func lspRoot(workDir, file string) string {
	dir := filepath.Dir(file)
	markers := []string{"go.mod", "pyproject.toml", "package.json", "Cargo.toml"}
	for {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if workDir != "" {
		return workDir
	}
	return filepath.Dir(file)
}

func severityName(n int) string {
	switch n {
	case 1:
		return "错误"
	case 2:
		return "警告"
	case 3:
		return "信息"
	case 4:
		return "提示"
	}
	return "诊断"
}
