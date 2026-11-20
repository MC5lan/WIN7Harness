//go:build windows && (amd64 || 386)

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 回归：cmd.exe 的命令串必须原样传给 /s /c。
//
// 旧实现用 exec.Command("cmd.exe", "/d", "/s", "/c", cmdline)，
// os/exec 会把 cmdline 里的引号二次转义成 \"，导致
// dir "D:\路径"、任何带空格/中文的路径都报“文件名、目录名或卷标语法不正确”。
func TestRunShellQuotedChinesePath(t *testing.T) {
	requireChineseOEM(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "中文目录"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, code, err := runShell(`dir /b /ad "`+root+`"`, "", 10*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || !strings.Contains(out, "中文目录") {
		t.Fatalf("带引号的中文路径执行失败: code=%d out=%q", code, out)
	}
}

func TestRunShellEchoDoubleQuotes(t *testing.T) {
	out, code, err := runShell(`echo "hello world"`, "", 10*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || strings.TrimSpace(out) != `"hello world"` {
		t.Fatalf("引号被二次转义: code=%d out=%q", code, out)
	}
}

// PowerShell -Command "... 多段引号 ..." 也是模型常用写法，必须原样传给 cmd。
func TestRunShellPowerShellQuotedCommand(t *testing.T) {
	requireChineseOEM(t)
	out, code, err := runShell(`powershell -NoProfile -Command "Write-Output '中文'"`, "", 20*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || !strings.Contains(out, "中文") {
		t.Fatalf("PowerShell 引号命令失败: code=%d out=%q", code, out)
	}
}
