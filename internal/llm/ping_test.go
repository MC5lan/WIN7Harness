package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Ping：/models 不可用（404/401，OpenAI 兼容网关常见）时退回一次极短对话；
// 极短对话只有一发、日志明示，不静默烧钱。
func TestPingFallsBackToChat(t *testing.T) {
	var chatCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
			atomic.AddInt32(&chatCalls, 1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"))
			w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New(srv.URL+"/v1", "k", "m", 5*time.Second)
	out, err := c.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping 应在极短对话后成功，实际报错：%v", err)
	}
	if !strings.Contains(out, "极短对话") {
		t.Fatalf("Ping 结果应说明 /models 不可用，实际：%q", out)
	}
	if n := atomic.LoadInt32(&chatCalls); n != 1 {
		t.Fatalf("应只发 1 次极短对话，实际 %d 次", n)
	}
}

// Ping：/models 401 但 chat 也 401 → 报错，且 chat 只被调用 1 次。
func TestPingChatAlsoFails(t *testing.T) {
	var chatCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		atomic.AddInt32(&chatCalls, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL+"/v1", "k", "m", 5*time.Second)
	_, err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("两个通道都 401，Ping 必须报错")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("错误应包含 /models 的状态码，实际：%v", err)
	}
	if n := atomic.LoadInt32(&chatCalls); n != 1 {
		t.Fatalf("应只发 1 次验证对话，实际 %d 次", n)
	}
}

// Ping：/models 正常 GET → 不发对话。
func TestPingNoChatWhenModelsOK(t *testing.T) {
	var chatCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":[{"id":"deepseek-chat"}]}`))
			return
		}
		atomic.AddInt32(&chatCalls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(srv.URL+"/v1", "k", "m", 5*time.Second)
	out, err := c.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping 应成功：%v", err)
	}
	if n := atomic.LoadInt32(&chatCalls); n != 0 {
		t.Fatalf("/models 正常时不应发对话，实际 %d 次", n)
	}
	if !strings.Contains(out, "连接成功") {
		t.Fatalf("意外输出：%q", out)
	}
}
