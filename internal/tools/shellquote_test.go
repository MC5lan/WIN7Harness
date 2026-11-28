package tools

import (
	"runtime"
	"strings"
	"testing"
)

// 目录名里的 & | 是 Windows 合法文件名字符，拼进 go test 前必须安全引用。
func TestQuoteShellArgBlocksCommandSeparators(t *testing.T) {
	arg := "./src&evil"
	got := quoteShellArg(arg)
	if !strings.Contains(got, arg) {
		t.Fatalf("引用后参数内容丢失：%q", got)
	}
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
			t.Fatalf("Windows 参数应加双引号：%q", got)
		}
	} else if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Fatalf("非 Windows 参数应加单引号：%q", got)
	}
}

func TestSafeShellArgRejectsExpansion(t *testing.T) {
	for _, s := range []string{`a%b`, `a!b`, `a"b`, "a\nb", ""} {
		if safeShellArg(s) {
			t.Fatalf("%q 应被拒绝", s)
		}
	}
	if !safeShellArg(`src&evil`) || !safeShellArg(`dir with space`) {
		t.Fatal("普通路径不应被拒绝")
	}
}

// run_tests 的 -run 值允许 Go 正则 |，但必须作为单个被引用的参数传给 go test，
// 不能被 cmd.exe 当成管道执行后面的命令。
func TestSanitizeExtraArgsQuotesPipe(t *testing.T) {
	got, err := sanitizeExtraArgs("-run TestA|TestB")
	if err != nil {
		t.Fatalf("合法 Go 正则不应报错：%v", err)
	}
	if !strings.Contains(got, quoteShellArg("TestA|TestB")) {
		t.Fatalf("正则值没有被安全引用：%q", got)
	}

	got, err = sanitizeExtraArgs("-run x|cmd /c calc")
	if err != nil {
		t.Fatalf("引用后不应拒绝：%v", err)
	}
	if !strings.Contains(got, quoteShellArg("x|cmd")) {
		t.Fatalf("管道字符没有被安全引用：%q", got)
	}
	if strings.Contains(got, "x|cmd /c calc") {
		t.Fatalf("注入串不应原样出现在命令行：%q", got)
	}
}

// 自动验证的目标包路径必须被引用，不能让目录名里的 & 变成命令分隔符。
func TestTestCommandQuotesPackagePath(t *testing.T) {
	cmd, ok := testCommand("go", "", "./src&evil")
	if !ok {
		t.Fatal("go 框架不应返回 false")
	}
	if !strings.Contains(cmd, quoteShellArg("./src&evil")) {
		t.Fatalf("包路径没有被安全引用：%q", cmd)
	}
}
