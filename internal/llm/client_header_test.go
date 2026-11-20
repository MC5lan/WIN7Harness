package llm

import (
	"context"
	"testing"
	"time"

	"win7harness/internal/core"
)

func TestOpenCodeGoSessionHeader(t *testing.T) {
	c := New("https://opencode.ai/zen/go/v1", "sk-test", "hy4-flash-free", time.Second)
	c.SessionID = "win7h-conv-123"
	req, err := c.newRequest(context.Background(), []byte(`{"model":"m","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("x-opencode-session"); got != "win7h-conv-123" {
		t.Fatalf("x-opencode-session = %q", got)
	}
	if got := req.Header.Get("User-Agent"); got != "WIN7Harness/"+core.Version {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestOtherEndpointHasNoOpenCodeSession(t *testing.T) {
	c := New("https://api.deepseek.com/v1", "sk-test", "deepseek-flash", time.Second)
	c.SessionID = "win7h-conv-123"
	req, err := c.newRequest(context.Background(), []byte(`{"model":"m","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Get("x-opencode-session"); got != "" {
		t.Fatalf("普通端点不应带 x-opencode-session，实际 %q", got)
	}
}
