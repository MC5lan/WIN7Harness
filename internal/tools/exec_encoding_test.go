//go:build windows && (amd64 || 386)

package tools

import (
	"strings"
	"testing"
	"time"
)

func TestDecodeConsoleTextGBK(t *testing.T) {
	// “中文”的 GBK 编码，模拟中文 Win7 上 dir/where 的管道输出。
	raw := string([]byte{0xD6, 0xD0, 0xCE, 0xC4})
	if got := decodeConsoleText(raw); got != "中文" {
		t.Fatalf("GBK 解码失败: %q", got)
	}
	if got := decodeConsoleText("C:\\Users\\foo"); got != "C:\\Users\\foo" {
		t.Fatalf("ASCII 不应被改动: %q", got)
	}
	if got := decodeConsoleText("中文 UTF-8"); got != "中文 UTF-8" {
		t.Fatalf("合法 UTF-8 不应二次转码: %q", got)
	}
}

// 真实走一遍 cmd.exe：中文 Win7 的 echo 输出是 GBK，工具层必须能读回来。
func TestRunShellDecodesChinese(t *testing.T) {
	requireChineseOEM(t)
	out, code, err := runShell(`echo 中文路径`, "", 10*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("exit=%d out=%q", code, out)
	}
	if !strings.Contains(out, "中文路径") {
		t.Fatalf("中文输出未正确解码: %q", out)
	}
}
