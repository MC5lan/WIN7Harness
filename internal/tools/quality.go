package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"win7harness/internal/core"
)

// 覆盖率与性能基线工具。
// 历史版本接受调用方传入的 `cmd` 字符串并直接裸给 cmd /c —— 那是任意命令执行入口，
// 完全绕过 run_command 的确认体系。现在命令模板全部固定，
// 调用方只能选 mode / profile / timeout，不存在可以注入命令的位置。
func init() {
	register(Tool{
		Name: "quality",
		Desc: "覆盖率与性能基线：mode=cover 跑测试并统计各包覆盖率；mode=bench-record 记录性能基线；mode=bench-check 与基线比较找出性能退化（阈值 20%）。命令不可自定义，统一为 go test。",
		Schema: `{
  "type":"object",
  "properties":{
    "mode":{"type":"string","description":"cover / bench-record / bench-check，默认 cover"},
    "profile":{"type":"string","description":"cover 模式下已有的覆盖率文件路径（仅限工作区或数据目录内），给出则不执行命令直接解析"},
    "timeout":{"type":"integer","description":"命令超时秒数，默认 600，上限 1800"}
  },
  "required":["mode"]
}`,
		Run: runQuality,
	})
}

func runQuality(ctx *Context, args map[string]interface{}) (Result, error) {
	mode := strings.TrimSpace(Get(args, "mode"))
	if mode == "" {
		mode = "cover"
	}
	timeout := GetInt(args, "timeout", 600)
	if timeout <= 0 {
		timeout = 600
	}
	if timeout > 1800 {
		timeout = 1800
	}
	profile := strings.TrimSpace(Get(args, "profile"))

	switch mode {
	case "cover":
		if profile == "" {
			dir := core.LocalDataDir()
			_ = os.MkdirAll(dir, 0o755)
			profile = filepath.Join(dir, fmt.Sprintf("cover-%s.out", time.Now().Format("20060102-150405")))
			if !safeCmdPath(profile) {
				return Result{}, fmt.Errorf("内部覆盖率文件路径不合法")
			}
			cmdStr := "go test ./... -coverprofile=" + quoteIfSpace(profile)
			if out, _, err := runCmdCapture(ctx, cmdStr, timeout); err != nil && !strings.Contains(out, "coverage:") {
				return Result{}, fmt.Errorf("跑测试失败：%v\n%s", err, tailLines(out, 20))
			}
		} else if err := checkCoverProfilePath(ctx, profile); err != nil {
			return Result{}, err
		}
		data, err := os.ReadFile(profile)
		if err != nil {
			return Result{}, fmt.Errorf("读取覆盖率文件失败：%v", err)
		}
		all, pkgs := core.ParseCoverProfile(string(data))
		return Result{Output: core.FormatCoverage(all, pkgs, 60), Title: "覆盖率"}, nil

	case "bench-record", "bench-check":
		cmdStr := "go test -run ^$ -bench . -benchmem ./..."
		out, _, err := runCmdCapture(ctx, cmdStr, timeout)
		if err != nil && strings.TrimSpace(out) == "" {
			return Result{}, fmt.Errorf("跑基准失败：%v", err)
		}
		cur := core.ParseBenchOutput(out)
		if len(cur) == 0 {
			return Result{}, fmt.Errorf("没解析到 Benchmark 结果，命令输出末尾：\n%s", tailLines(out, 15))
		}
		bl, _ := core.LoadBenchBaseline(core.BaselinePath())
		if mode == "bench-record" {
			nb := &core.BenchBaseline{Cases: map[string]core.BenchCase{}}
			for _, c := range cur {
				nb.Cases[c.Name] = c
			}
			if err := core.SaveBenchBaseline(core.BaselinePath(), nb); err != nil {
				return Result{}, err
			}
			return Result{Output: fmt.Sprintf("已记录 %d 项性能基线（%s / %s）\n", len(cur), runtime.GOOS, runtime.GOARCH), Title: "性能基线"}, nil
		}
		if bl == nil || len(bl.Cases) == 0 {
			return Result{Output: core.FormatBenchDeltas(nil), Title: "性能基线"}, nil
		}
		return Result{Output: core.FormatBenchDeltas(core.CompareBench(bl.Cases, cur)), Title: "性能对比"}, nil
	}
	return Result{}, fmt.Errorf("不支持的 mode：%s（可用 cover / bench-record / bench-check）", mode)
}

// checkCoverProfilePath 限制 profile 只能来自工作区或数据目录，
// 防止模型拿任意文件当覆盖率喂进解析器。
func checkCoverProfilePath(ctx *Context, p string) error {
	if !safeCmdPath(p) {
		return fmt.Errorf("profile 路径含有不允许的字符")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("profile 路径无效：%v", err)
	}
	wd, err := filepath.Abs(ctx.WorkDir)
	if err != nil {
		return fmt.Errorf("工作目录无效：%v", err)
	}
	dd := core.LocalDataDir()
	if !withinDir(wd, abs) && !withinDir(dd, abs) {
		return fmt.Errorf("profile 必须在工作区或数据目录内")
	}
	return nil
}

// withinDir 判断 path 是否位于 dir 内（含 dir 本身）。
func withinDir(dir, path string) bool {
	dir = filepath.Clean(dir)
	path = filepath.Clean(path)
	if strings.EqualFold(dir, path) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(path), strings.ToLower(dir)+string(filepath.Separator))
}

// safeCmdPath 白名单校验可以被拼进命令行的路径字符：
// 盘符/路径/点横线下划线与空格。任何其它字符（& | < > % " 等）一律拒绝。
func safeCmdPath(p string) bool {
	if p == "" {
		return false
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == ' ' || r == '.' || r == '-' || r == '_':
		case r == '\\' || r == '/' || r == ':':
		default:
			return false
		}
	}
	return true
}

// runCmdCapture 执行命令并返回合并输出；带超时。
// 走 runShell（Job Object + taskkill /T 杀树 + CREATE_NO_WINDOW），
// 命令串只可能是内部固定模板，调用方无法注入。
func runCmdCapture(ctx *Context, cmdStr string, timeout int) (string, int, error) {
	return runShell(cmdStr, ctx.WorkDir, time.Duration(timeout)*time.Second, nil)
}

func quoteIfSpace(s string) string {
	if strings.ContainsAny(s, " ") {
		return "\"" + s + "\""
	}
	return s
}
