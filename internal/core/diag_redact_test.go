package core

import (
	"strings"
	"testing"
)

func TestMaskURLSecrets(t *testing.T) {
	in := "https://user:pass@gateway.example/v1?api_key=sk-secret&model=x&token=abc"
	got := maskURLSecrets(in)
	for _, leak := range []string{"pass", "sk-secret", "abc"} {
		if strings.Contains(got, leak) {
			t.Fatalf("URL 脱敏遗漏 %q：%s", leak, got)
		}
	}
	if !strings.Contains(got, "gateway.example") || !strings.Contains(got, "model=x") {
		t.Fatalf("非敏感部分不应被破坏：%s", got)
	}
}

func TestRedactMCPCommand(t *testing.T) {
	got := redactArgs([]string{"server --api-key=sk-secret TOKEN=abc"})
	if strings.Contains(got[0], "sk-secret") || strings.Contains(got[0], "abc") {
		t.Fatalf("MCP 命令行脱敏失败：%s", got[0])
	}
}