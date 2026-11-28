package tools

import (
	"context"
	"testing"
	"time"
)

// 启动失败不能留下缓存项，否则后续调用会拿到 nil/dead client。
func TestGetLSPClientDoesNotCacheFailedStart(t *testing.T) {
	ShutdownLSP()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key, entry, err := getLSPClient(ctx, "definitely-not-a-real-lsp-binary", nil, t.TempDir())
	if err == nil || entry != nil || key != "" {
		t.Fatalf("不存在的语言服务器应返回错误且不缓存：key=%q entry=%v err=%v", key, entry, err)
	}
	lspPoolMu.Lock()
	n := len(lspPool)
	lspPoolMu.Unlock()
	if n != 0 {
		t.Fatalf("失败启动不应留下缓存项，实际 %d", n)
	}
}

func TestShutdownLSPEmpty(t *testing.T) {
	ShutdownLSP()
	ShutdownLSP()
}
