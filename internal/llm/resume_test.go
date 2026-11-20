package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 断线续传：第一次请求只吐一段就被掐断，第二次请求应该带上"已输出内容 + 续写指令"，
// 两段正文拼起来交给上层，token 用量累加。
func TestChatResumeAfterDisconnect(t *testing.T) {
	var calls int32
	var secondBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			if err := json.NewDecoder(r.Body).Decode(&map[string]interface{}{}); err != nil {
				t.Logf("解码请求失败: %v", err)
			}
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("不支持 Hijack，测试环境异常")
				return
			}
			conn, buf, err := hj.Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n")
			buf.WriteString(`data: {"choices":[{"delta":{"content":"前半段"}}]}` + "\n\n")
			buf.Flush()
			conn.Close()
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&secondBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"后半段"}}]}` + "\n\n"))
		w.Write([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	c := New(srv.URL+"/v1", "k", "m", 5*time.Second)

	var got strings.Builder
	usage, err := c.Chat(context.Background(),
		[]Message{{Role: "user", Content: "写一段话"}}, nil, 0.3, 512,
		Handlers{OnContent: func(s string) { got.WriteString(s) }})
	if err != nil {
		t.Fatalf("应自动续写成功，实际报错: %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Fatalf("应发起 2 次请求，实际 %d", n)
	}
	if got.String() != "前半段后半段" {
		t.Fatalf("正文拼接不对: %q", got.String())
	}
	if usage.CompletionTokens != 5 {
		t.Fatalf("用量统计不对: %+v", usage)
	}

	// 第二次请求必须带上断点信息
	msgs, _ := secondBody["messages"].([]interface{})
	if len(msgs) != 3 {
		t.Fatalf("续写请求应有 3 条消息（原始 + 已输出 + 续写指令），实际 %d", len(msgs))
	}
	last, _ := msgs[2].(map[string]interface{})
	if !strings.Contains(last["content"].(string), "不要重复") {
		t.Fatalf("续写指令缺失: %v", last)
	}
	mid, _ := msgs[1].(map[string]interface{})
	if mid["role"] != "assistant" || mid["content"] != "前半段" {
		t.Fatalf("已输出内容未带上: %v", mid)
	}
}

// 首包就断（一个字都没收到）时不该续写，直接报错。
func TestChatNoResumeWithoutContent(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		hj := w.(http.Hijacker)
		conn, buf, _ := hj.Hijack()
		buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\n")
		buf.Flush()
		conn.Close()
	}))
	defer srv.Close()

	c := New(srv.URL+"/v1", "k", "m", 5*time.Second)
	_, err := c.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, 0.3, 128, Handlers{})
	if err == nil {
		t.Fatal("没收到内容时应报错")
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("不应续写，实际请求 %d 次", n)
	}
}

func TestCanResumeRules(t *testing.T) {
	if canResume(context.Background(), context.Canceled, "有内容") {
		t.Fatal("用户取消不应续写")
	}
	if canResume(context.Background(), errNoResume, "   ") {
		t.Fatal("没有正文不应续写")
	}
	if canResume(context.Background(), errPartialToolCall, "有内容") {
		t.Fatal("工具调用被截断不应续写")
	}
	if canResume(context.Background(), &APIError{Status: 401, Message: "bad key"}, "有内容") {
		t.Fatal("鉴权错误不应续写")
	}
	if !canResume(context.Background(), &APIError{Status: 429, Message: "限流"}, "有内容") {
		t.Fatal("限流应允许续写")
	}
	if canResume(context.Background(), context.DeadlineExceeded, "有内容") {
		t.Fatal("整体超时不应续写（时间预算已用尽）")
	}
}
