package tools

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"win7harness/internal/core"
)

func init() {
	register(Tool{
		Name: "diagnostics",
		Desc: "编译/静态诊断：Go 跑 go vet ./...（会暴露编译错误与常见静态问题）；Rust 跑 cargo check；Python 跑 compileall。改完单文件、提交前先用它，比等全量测试更快。",
		Schema: `{
  "type":"object",
  "properties":{
    "path":{"type":"string","description":"项目目录，默认当前工作目录"},
    "max_results":{"type":"integer","description":"最多返回多少条诊断，默认 40"}
  }
}`,
		Run: runDiagnostics,
	})
}

func runDiagnostics(ctx *Context, args map[string]interface{}) (Result, error) {
	root := coreResolveDiagnostics(ctx, Get(args, "path"))
	if err := guardSearchRoot(root); err != nil {
		return Result{}, err
	}
	maxN := GetInt(args, "max_results", 40)
	if maxN <= 0 || maxN > 200 {
		maxN = 40
	}
	start := time.Now()
	var cmd, out string
	var code int
	var err error
	switch {
	case fileExists(filepath.Join(root, "go.mod")):
		if tr := CheckGoToolchain(root); !tr.OK() {
			return Result{Output: tr.Reason(), Display: true, Title: "编译诊断（环境不支持）"}, nil
		}
		cmd = "go vet ./... 2>&1"
		out, code, err = runShell(cmd, root, 120*time.Second, nil, ctxDone(ctx))
	case fileExists(filepath.Join(root, "Cargo.toml")):
		cmd = "cargo check --message-format short 2>&1"
		out, code, err = runShell(cmd, root, 180*time.Second, nil, ctxDone(ctx))
	case fileExists(filepath.Join(root, "pyproject.toml")) || fileExists(filepath.Join(root, "requirements.txt")):
		cmd = "python -m compileall -q . 2>&1"
		out, code, err = runShell(cmd, root, 60*time.Second, nil, ctxDone(ctx))
	default:
		return Result{}, fmt.Errorf("无法识别项目类型（需要 go.mod / Cargo.toml / pyproject.toml / requirements.txt）：%s", root)
	}
	if err != nil && out == "" {
		return Result{}, fmt.Errorf("诊断命令无法启动：%v", err)
	}
	lines := diagnosticsLines(out, maxN)
	head := fmt.Sprintf("诊断命令：%s\n耗时：%s\n", cmd, time.Since(start).Round(time.Millisecond))
	if code == 0 && len(lines) == 0 {
		return Result{Output: head + "结果：通过，没有发现诊断问题。", Display: true, Title: "编译诊断"}, nil
	}
	if len(lines) == 0 {
		lines = []string{fmt.Sprintf("命令退出码 %d，但没有可解析的诊断行；原始输出末尾：", code), tailLines(out, 20)}
	}
	return Result{Output: head + strings.Join(lines, "\n"), Display: true, Title: fmt.Sprintf("编译诊断（%d 条）", len(lines))}, nil
}

func coreResolveDiagnostics(ctx *Context, p string) string {
	if strings.TrimSpace(p) == "" {
		return ctx.WorkDir
	}
	return core.ResolvePath(ctx.WorkDir, p)
}

// diagnosticsLines 提取 file:line[:col]: message 形式的诊断，并限制条数。
func diagnosticsLines(out string, maxN int) []string {
	var lines []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		ln := strings.TrimSpace(raw)
		if ln == "" || strings.HasPrefix(ln, "#") || strings.HasPrefix(ln, "go: ") {
			continue
		}
		// 保留编译器/静态检查的行；纯日志噪声跳过。
		if !strings.Contains(ln, ".go:") && !strings.Contains(ln, ".rs:") && !strings.Contains(ln, ".py:") {
			continue
		}
		if seen[ln] {
			continue
		}
		seen[ln] = true
		lines = append(lines, ln)
		if len(lines) >= maxN {
			break
		}
	}
	return lines
}
