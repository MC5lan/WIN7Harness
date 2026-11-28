package tools

import (
	"runtime"
	"strings"
)

// quoteShellArg 把单个参数包进当前平台的 shell 引号。
// 只用于必须经 runShell 的少量参数；能直接 argv 调用的工具仍应优先 argv。
func quoteShellArg(s string) string {
	if runtime.GOOS == "windows" {
		// cmd.exe：双引号内的 & | < > ^ ( ) 不再解释。Windows 文件名不允许 "，
		// 因此这里只需把内嵌引号加倍，避免破坏引号边界。
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// safeShellArg 拒绝在双引号内仍可能被 cmd.exe 展开/截断的字符。
// % 在引号内仍会展开；! 只在 delayed expansion 下展开，但这里一并拒绝，
// 避免调用方日后给 runShell 加 /v:on 时留下隐患。
func safeShellArg(s string) bool {
	if s == "" || strings.ContainsAny(s, "\x00\r\n%!\"") {
		return false
	}
	return true
}

// quotePackageArgs 把空格分隔的包路径逐个安全引用；任一参数不安全就返回 false，
// 调用方应退回全量测试，而不是拼出半截命令行。
func quotePackageArgs(s string) (string, bool) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", false
	}
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if !safeShellArg(f) {
			return "", false
		}
		out = append(out, quoteShellArg(f))
	}
	return strings.Join(out, " "), true
}
