package tools

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"win7harness/internal/core"
)

// 钩子事件名。
const (
	HookPreTool  = "pre_tool"
	HookPostTool = "post_tool"
	HookTurnEnd  = "turn_end"
)

// hookMatch 判断工具名是否命中钩子的 match 规则：* 通配、逗号分隔、前缀通配 write_*。
func hookMatch(pattern, tool string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || pattern == "*" {
		return true
	}
	for _, one := range strings.Split(pattern, ",") {
		one = strings.TrimSpace(one)
		if one == "" {
			continue
		}
		if one == "*" || one == tool {
			return true
		}
		if strings.HasSuffix(one, "*") && strings.HasPrefix(tool, strings.TrimSuffix(one, "*")) {
			return true
		}
	}
	return false
}

// hookEnv 返回注入钩子命令的 HARNESS_* 环境变量。
func hookEnv(event, tool, workdir string) []string {
	return []string{
		"HARNESS_EVENT=" + event,
		"HARNESS_TOOL=" + tool,
		"HARNESS_WORKDIR=" + workdir,
	}
}

// RunHooks 执行某个事件下的钩子命令，返回是否阻止后续动作以及要展示给模型的文本。
// pre_tool 的钩子以非零退出码结束即视为阻止。
func RunHooks(event, tool string, ctx *Context) (block bool, note string) {
	if ctx == nil {
		return false, ""
	}
	hooks := core.HooksSnapshot()
	if len(hooks) == 0 {
		return false, ""
	}
	var b strings.Builder
	for _, h := range hooks {
		if !strings.EqualFold(strings.TrimSpace(h.Event), event) {
			continue
		}
		if tool != "" && !hookMatch(h.Match, tool) {
			continue
		}
		cmdline := strings.TrimSpace(h.Command)
		if cmdline == "" {
			continue
		}
		timeout := h.TimeoutSec
		if timeout <= 0 {
			timeout = 30
		}
		name := tool
		if name == "" {
			name = "-"
		}
		line := fmt.Sprintf("[hook %s/%s] $ %s", event, name, cmdline)
		if ctx.Progress != nil {
			ctx.Progress(line)
		}
		start := time.Now()
		// HARNESS_* 环境变量经 extraEnv 传入（不拼进命令行）：
		// 值含 %VAR%、空格、引号都不再参与 shell 解析，也不存在拼接注入。
		out, code, err := runShell(cmdline, ctx.WorkDir, time.Duration(timeout)*time.Second, hookEnv(event, name, ctx.WorkDir), ctxDone(ctx))
		used := time.Since(start).Milliseconds()
		out = sanitizeHookOutput(out, 4000)
		b.WriteString(fmt.Sprintf("%s\n退出码: %d，耗时: %dms\n", line, code, used))
		if out != "" {
			b.WriteString(out + "\n")
		}
		if err != nil && code == 0 {
			b.WriteString("错误: " + err.Error() + "\n")
		}
		if code != 0 && event == HookPreTool {
			block = true
		}
	}
	note = strings.TrimRight(b.String(), "\n")
	return block, note
}

// sanitizeHookOutput 清理钩子输出后再回注给模型：
//   - 去掉 ANSI 转义序列（ESC[…字母 / OSC…BEL），否则 ESC 序列会直接进上下文和终端；
//   - 除 \t\n 外的控制字符替换为空格；
//   - 按 rune 截断到 max 字符（此前按字节截断会把汉字从中间切开）。
var reANSI = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]|\x1b\\][^\x07\x1b]*(\x07|\x1b\\\\)")

func sanitizeHookOutput(s string, max int) string {
	s = reANSI.ReplaceAllString(s, "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return r
		case r < 0x20 || r == 0x7f:
			return ' '
		default:
			return r
		}
	}, s)
	s = strings.TrimSpace(s)
	r := []rune(s)
	if max > 0 && len(r) > max {
		return string(r[:max]) + "…（已截断）"
	}
	return s
}

// HooksSummary 返回钩子配置的只读摘要，供 /hooks 与 /status 展示。
func HooksSummary() []string {
	hooks := core.HooksSnapshot()
	out := make([]string, 0, len(hooks))
	for _, h := range hooks {
		t := h.TimeoutSec
		if t <= 0 {
			t = 30
		}
		out = append(out, fmt.Sprintf("%s  match=%-16s timeout=%ds  $ %s", h.Event, h.Match, t, h.Command))
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
