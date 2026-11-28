package mcp

import (
	"bufio"
	"strings"
	"testing"
)

func TestMCPBaseEnvFiltersSecrets(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-should-not-leak")
	t.Setenv("GITHUB_TOKEN", "ghp-should-not-leak")
	env := mcpBaseEnv()
	for _, kv := range env {
		up := strings.ToUpper(kv)
		if strings.HasPrefix(up, "OPENAI_API_KEY=") || strings.HasPrefix(up, "GITHUB_TOKEN=") {
			t.Fatalf("MCP 子进程不应继承敏感变量：%s", kv)
		}
	}
}

func TestReadMCPLineRejectsOversize(t *testing.T) {
	long := strings.Repeat("a", maxMCPLineBytes+1) + "\n"
	r := bufio.NewReaderSize(strings.NewReader(long), 64*1024)
	if _, err := readMCPLine(r); err == nil {
		t.Fatal("超长 MCP 行应被拒绝")
	}
}

func TestReadMCPLineNormal(t *testing.T) {
	r := bufio.NewReaderSize(strings.NewReader("{\"ok\":true}\n"), 64*1024)
	got, err := readMCPLine(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != `{"ok":true}` {
		t.Fatalf("读取结果异常：%q", got)
	}
}
