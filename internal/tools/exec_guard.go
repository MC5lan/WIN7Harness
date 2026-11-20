package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// driveRootRe 匹配盘符根（C:\、D:\ 后面是空格/引号/结尾/命令分隔符）。
var driveRootRe = regexp.MustCompile(`(?i)[a-z]:\\(?:\s|"|'|$|&|\||\*|\?)`)

// guardBroadCommand 拦住“全盘搜索”类命令。
//
// 触发这次闪退的不是 glob 本身，而是模型为了找程序/技能，先 glob C:\Users，
// 又执行 where /r C:\ 扫了 60 秒。这里在命令进入 shell 前直接拒绝，
// 并告诉模型正确的替代方案，省时间也省 token。
func guardBroadCommand(cmdline string) error {
	low := strings.ToLower(strings.TrimSpace(cmdline))
	if low == "" {
		return nil
	}
	broad := false
	switch {
	case strings.Contains(low, "where /r"),
		strings.Contains(low, "where.exe /r"),
		strings.Contains(low, "findstr /s"),
		strings.Contains(low, "forfiles /s"),
		strings.Contains(low, "tree "):
		broad = true
	case strings.Contains(low, "dir ") && strings.Contains(low, " /s"):
		broad = true
	case (strings.Contains(low, "get-childitem") || strings.Contains(low, "gci ")) &&
		(strings.Contains(low, "-recurse") || strings.Contains(low, " -r ")):
		broad = true
	}
	if !broad {
		return nil
	}
	if driveRootRe.MatchString(low) || strings.Contains(low, "%systemdrive%") {
		return fmt.Errorf("拒绝全盘搜索（%s）：请用 list_dir / glob 指向具体目录，或直接读运行环境里已知的数据/技能/程序路径；查 PATH 里的程序用 where <名字>，不要 /r C:\\", cmdline)
	}
	return nil
}
