package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// finish_reason=length 必须透传给 Handlers，Agent 才能丢弃截断的工具调用。
func TestChatReportsFinishReasonLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := New(srv.URL, "", "test-model", 5*time.Second)
	got := ""
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, 0.2, 128, Handlers{
		OnFinish: func(reason string) { got = reason },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "length" {
		t.Fatalf("finish_reason 未透传：%q", got)
	}
}
