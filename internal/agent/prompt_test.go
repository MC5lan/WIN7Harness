package agent

import (
	"strings"
	"testing"
)

// 内嵌提示词是跨版本兼容性的一部分：Win7 缺了这些红线，模型会开始建议
// winget/pwsh 7；Win10/11 继续套 Win7 红线，则会把现代用户项目强行降级。
func TestWin7RuntimePromptGuardrails(t *testing.T) {
	s := runtimePromptFor(true)
	for _, want := range []string{
		"Windows 7 SP1",
		"cmd.exe",
		"PowerShell 2.0",
		"winget",
		"260 字符",
		"Go 1.20.14",
		"Token 纪律",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("Win7 提示缺少 %q", want)
		}
	}
}

func TestModernRuntimePromptDoesNotForceWin7(t *testing.T) {
	s := runtimePromptFor(false)
	for _, want := range []string{
		"Windows 10/11",
		"toolchain",
		"Go 1.20.14",
		"用户项目",
		"Token 纪律",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("Win10/11 提示缺少 %q", want)
		}
	}
	if strings.Contains(s, "宿主是 Windows 7 SP1") {
		t.Fatal("Win10/11 提示不应把宿主说成 Win7")
	}
}

func TestSystemPromptHasEnvironment(t *testing.T) {
	s := SystemPrompt(`C:\work`)
	for _, want := range []string{
		"操作系统:",
		"工作目录: C:\\work",
		"Token 纪律",
		"全局技能目录",
		"运行日志",
		"不要直接新建文件或技能",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("系统提示缺少 %q", want)
		}
	}
}
