//go:build windows

package win32

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestSystemRootsPEM(t *testing.T) {
	b, err := SystemRootsPEM()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("BEGIN CERTIFICATE")) {
		t.Fatal("系统证书库导出为空")
	}
}

// TestResolvePACProxyLive 需要手工设置 PAC 后显式打开：
//
//	WIN7H_TEST_PAC=1 go test ./internal/win32 -run TestResolvePACProxyLive -v
//
// PAC 内容应把 pac-test.example 指向 127.0.0.1:18080，其余 DIRECT。
func TestResolvePACProxyLive(t *testing.T) {
	if os.Getenv("WIN7H_TEST_PAC") != "1" {
		t.Skip("设置 WIN7H_TEST_PAC=1 并配置测试 PAC 后运行")
	}
	proxy, _, direct, ok := ResolvePACProxy("http://pac-test.example/")
	if !ok || direct || !strings.Contains(proxy, "127.0.0.1:18080") {
		t.Fatalf("PAC 未解析到测试代理：proxy=%q direct=%v ok=%v", proxy, direct, ok)
	}
}
