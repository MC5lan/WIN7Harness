//go:build windows

package tools

import (
	"os"
	"strings"
	"sync"
	"testing"

	"win7harness/internal/core"
	"win7harness/internal/netbridge"
)

func TestChildNetEnvExportsCABundle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", home)
	// 重置缓存，让测试使用临时 home。
	caOnce = sync.Once{}
	caPath = ""
	caEnough = false

	env := childNetEnv()
	found := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "GIT_SSL_CAINFO=") {
			found = strings.TrimPrefix(kv, "GIT_SSL_CAINFO=")
		}
	}
	if found == "" {
		t.Fatalf("没有注入 GIT_SSL_CAINFO；env=%v", env)
	}
	st, err := os.Stat(found)
	if err != nil || st.Size() == 0 {
		t.Fatalf("CA 文件不存在或为空：%s (%v)", found, err)
	}
}

func TestChildNetEnvUsesLocalBridgeForManualProxy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", home)
	core.Load()
	cfg := core.Get()
	oldMode, oldURL := cfg.ProxyMode, cfg.ProxyURL
	cfg.ProxyMode, cfg.ProxyURL = "manual", "http://127.0.0.1:9"
	// 父进程已有上游代理时，桥仍必须覆盖它，否则 NTLM 子进程会绕过桥。
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	caOnce = sync.Once{}
	caPath = ""
	caEnough = false
	netbridge.Stop()
	defer func() {
		cfg.ProxyMode, cfg.ProxyURL = oldMode, oldURL
		netbridge.Stop()
	}()

	env := childNetEnv()
	found := ""
	for _, kv := range env {
		if strings.HasPrefix(kv, "HTTPS_PROXY=") {
			found = strings.TrimPrefix(kv, "HTTPS_PROXY=")
		}
	}
	if !strings.HasPrefix(found, "http://127.0.0.1:") {
		t.Fatalf("手动代理没有走本机桥：HTTPS_PROXY=%q", found)
	}
}
