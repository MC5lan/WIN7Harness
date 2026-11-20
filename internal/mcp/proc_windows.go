//go:build windows

package mcp

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"win7harness/internal/win32"
)

// killProcessTreePS 按 PID 树收割（MCP 的 npx/uvx 经常派生孙进程）。
func killProcessTreePS(pid int) {
	c := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	_ = c.Run()
}

func assignJob(pid int) (uintptr, bool) { return win32.AssignKillOnCloseJob(pid) }

func terminateJob(job uintptr) bool { return win32.TerminateJob(job, 1) }

func closeHandle(h uintptr) { win32.CloseHandle(h) }

// newMCPCommand Win7 下 npx/pnpm/uvx 是 .cmd，必须经 cmd.exe 启动。
//
// 三条规则（此前的 \“ 转义对 cmd.exe 无效，含引号参数可以逃逸注入任意命令）：
//  1. 参数里不允许出现引号（双引号会破坏 cmd 的引号配对，这是注入的根）；
//     参数也不允许出现 %（%VAR% 在引号内仍会展开）；
//  2. 命令行整体再包一层外层引号（cmd /d /s /c ""...""）：
//     /s 只剥离最外层首尾引号，内层结构原样传给 .cmd；
//  3. 命令本身只允许常见字符（盘符、路径、字母数字、. _ - 空格），其它一律拒绝。
func newMCPCommand(command string, args []string) (*exec.Cmd, error) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return nil, fmt.Errorf("命令为空")
	}
	checkCommandToken := func(s string) error {
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			case r == ' ' || r == '.' || r == '-' || r == '_' || r == ':' || r == '\\' || r == '/':
			default:
				return fmt.Errorf("命令名含有不允许的字符 %q（仅字母数字与 . _ - : \\ / 空格）", r)
			}
		}
		return nil
	}
	if err := checkCommandToken(cmd); err != nil {
		return nil, err
	}
	// 参数白名单：无引号、无 %、无换行/制表符；允许空格与常见 URL/包名字符。
	checkArg := func(s string) error {
		if strings.ContainsAny(s, "\"'%\r\n\t") {
			return fmt.Errorf("参数含有引号或 %%，已拒绝（%q）", clipForError(s))
		}
		return nil
	}
	for _, a := range args {
		if err := checkArg(a); err != nil {
			return nil, err
		}
	}

	// .exe/.com 直接起，跳过 cmd 层
	lower := strings.ToLower(cmd)
	if strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".com") {
		return exec.Command(cmd, args...), nil
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteForCmd(cmd))
	for _, a := range args {
		parts = append(parts, quoteForCmd(a))
	}
	// 整行再包一层引号："... "x y" ... "
	// /s 剥离外层首尾引号后，内层引号保留，.cmd 拿到的就是正确拆分的参数。
	line := `""` + strings.Join(parts, " ") + `""`
	return exec.Command("cmd.exe", "/d", "/s", "/c", line), nil
}

// quoteForCmd 含空格或 & | < > ^ 的参数加双引号（此类字符在双引号内是字面量）。
func quoteForCmd(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t&|<>^()") {
		return `"` + s + `"`
	}
	return s
}

func clipForError(s string) string {
	const max = 40
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

func hideMCPWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
