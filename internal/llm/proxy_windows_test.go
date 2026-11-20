//go:build windows

package llm

import (
	"net/url"
	"strings"
	"testing"
)

func TestResolveProxyLoopbackBypassesUpstream(t *testing.T) {
	// 本机桥是显式 Proxy，不会自动套用 ProxyFromEnvironment 的 localhost 绕过；
	// 这里保证本地模型/测试服务器不会被送到公司代理。
	u, _ := url.Parse("http://127.0.0.1:12345/v1")
	if p, err := ResolveProxyForURL(u); err != nil || p != nil {
		t.Fatalf("127.0.0.1 应直连，得到 %v / %v", p, err)
	}
	u, _ = url.Parse("http://[::1]:12345/")
	if p, err := ResolveProxyForURL(u); err != nil || p != nil {
		t.Fatalf("::1 应直连，得到 %v / %v", p, err)
	}
}

func TestNoProxyList(t *testing.T) {
	got := noProxyList("<local>;*.corp.example;10.*;localhost")
	for _, want := range []string{"localhost", "127.0.0.1", ".local", "corp.example", "10.*"} {
		if !strings.Contains(got, want) {
			t.Fatalf("NO_PROXY=%q 缺少 %q", got, want)
		}
	}
	if strings.Contains(got, "<local>") || strings.Contains(got, "*.corp.example") {
		t.Fatalf("NO_PROXY 仍是 WinINET 语法：%q", got)
	}
}
