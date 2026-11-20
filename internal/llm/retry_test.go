package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(url string) *Client {
	c := New(url, "test-key", "test-model", 5*time.Second)
	c.RetryBase = 5 * time.Millisecond
	return c
}

func TestDoRetriesOn429ThenSucceeds(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"rate limited"}}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer srv.Close()

	resp, err := newTestClient(srv.URL).do(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("期望重试后成功，实际错误：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if n := atomic.LoadInt32(&hits); n != 3 {
		t.Fatalf("请求次数 = %d，期望 3（1 次原始 + 2 次重试）", n)
	}
}

func TestDoRetriesOn503Exhausted(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":{"message":"upstream down"}}`)
	}))
	defer srv.Close()

	c := newTestClient(srv.URL)
	_, err := c.do(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("期望最终返回错误")
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusServiceUnavailable {
		t.Fatalf("错误类型不符：%v", err)
	}
	if n := atomic.LoadInt32(&hits); n != 4 {
		t.Fatalf("请求次数 = %d，期望 4（默认重试 3 次）", n)
	}
}

func TestDoNoRetryOn400(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"bad model"}}`)
	}))
	defer srv.Close()

	_, err := newTestClient(srv.URL).do(context.Background(), []byte(`{"model":"x"}`))
	if err == nil {
		t.Fatal("期望返回错误")
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("400 不应重试，实际请求 %d 次", n)
	}
}

func TestDoNoRetryWhenCanceled(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := newTestClient(srv.URL).do(ctx, []byte(`{}`))
	if err == nil {
		t.Fatal("期望返回错误")
	}
	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("已取消的上下文不应发起请求，实际 %d 次", n)
	}
}

func TestBackoffDoublesAndCaps(t *testing.T) {
	base := time.Second
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	for i, w := range want {
		got := backoff(base, i)
		lo := time.Duration(float64(w) * 0.75)
		hi := time.Duration(float64(w) * 1.25)
		if hi > maxRetryDelay {
			hi = maxRetryDelay
		}
		if got < lo || got > hi {
			t.Fatalf("第 %d 次退避 = %v，期望 [%v, %v]（封顶 %v，±25%% 抖动）", i, got, lo, hi, maxRetryDelay)
		}
	}
}

func TestRetryAfterHeader(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "3")
	if d := retryAfter(h); d != 3*time.Second {
		t.Fatalf("Retry-After 解析 = %v，期望 3s", d)
	}
	h.Set("Retry-After", "999")
	if d := retryAfter(h); d != 60*time.Second {
		t.Fatalf("Retry-After 应封顶到 60s，实际 %v", d)
	}
	h.Del("Retry-After")
	if d := retryAfter(h); d != 0 {
		t.Fatalf("无头部应返回 0，实际 %v", d)
	}
}

func TestRetryableStatus(t *testing.T) {
	yes := []int{408, 425, 429, 500, 502, 503, 504, 529}
	for _, c := range yes {
		if !retryableStatus(c) {
			t.Fatalf("%d 应可重试", c)
		}
	}
	no := []int{200, 400, 401, 403, 404, 422}
	for _, c := range no {
		if retryableStatus(c) {
			t.Fatalf("%d 不应重试", c)
		}
	}
}

func TestFriendlyNetErr(t *testing.T) {
	cases := map[string]string{
		"x509: certificate signed by unknown authority":        "证书",
		"dial tcp: lookup api.x.com: no such host":             "域名解析",
		"dial tcp 127.0.0.1:8080: connect: connection refused": "连接被拒绝",
		"context deadline exceeded: timeout":                   "超时",
	}
	for in, want := range cases {
		got := friendlyNetErr(errors.New(in))
		if !strings.Contains(got.Error(), want) {
			t.Fatalf("%q 的提示应包含 %q，实际 %q", in, want, got.Error())
		}
		if !strings.Contains(got.Error(), in) {
			t.Fatalf("提示应保留原始错误，实际 %q", got.Error())
		}
	}
	if err := friendlyNetErr(errors.New("weird")); err == nil || err.Error() != "weird" {
		t.Fatalf("未知错误应原样返回，实际 %v", err)
	}
}

func TestParseProxyServer(t *testing.T) {
	cases := []struct {
		server, scheme, want string
	}{
		{"127.0.0.1:7890", "https", "127.0.0.1:7890"},
		{"http=10.0.0.1:3128;https=10.0.0.1:3129", "https", "10.0.0.1:3129"},
		{"http=10.0.0.1:3128;https=10.0.0.1:3129", "http", "10.0.0.1:3128"},
		{"socks=127.0.0.1:1080", "https", "127.0.0.1:1080"},
		{"", "http", ""},
		{"http=10.0.0.1:3128", "https", "10.0.0.1:3128"},
	}
	for _, c := range cases {
		if got := parseProxyServer(c.server, c.scheme); got != c.want {
			t.Fatalf("parseProxyServer(%q,%q) = %q，期望 %q", c.server, c.scheme, got, c.want)
		}
	}
}

func TestProxyURLFor(t *testing.T) {
	u := proxyURLFor(ProxySetting{Enabled: true, Server: "127.0.0.1:7890"}, "https")
	if u == nil || u.String() != "http://127.0.0.1:7890" {
		t.Fatalf("普通代理解析结果 = %v", u)
	}
	u = proxyURLFor(ProxySetting{Enabled: true, Server: "socks=127.0.0.1:1080"}, "https")
	if u == nil || u.Scheme != "socks5" {
		t.Fatalf("socks 代理应转成 socks5://，实际 %v", u)
	}
	if u := proxyURLFor(ProxySetting{Enabled: false, Server: "127.0.0.1:7890"}, "https"); u != nil {
		t.Fatalf("未启用时应返回 nil，实际 %v", u)
	}
}

func TestBypassHost(t *testing.T) {
	ov := "localhost;127.0.0.1;*.corp.example.com;intranet;<local>"
	cases := map[string]bool{
		"localhost":            true,
		"api.corp.example.com": true,
		"corp.example.com":     true,
		"intranet":             true,
		"api.deepseek.com":     false,
		"127.0.0.1":            true,
	}
	for host, want := range cases {
		if got := bypassHost(ov, host); got != want {
			t.Fatalf("bypassHost(%q) = %v，期望 %v", host, got, want)
		}
	}
	if bypassHost("", "api.deepseek.com") {
		t.Fatal("空绕过列表不应绕过任何主机")
	}
	if !bypassHost("<local>", "fileserver") {
		t.Fatal("<local> 应绕过无点短名")
	}
	if bypassHost("<local>", "a.b.com") {
		t.Fatal("<local> 不应绕过完整域名")
	}
}

func TestProxyFuncNilWhenDisabled(t *testing.T) {
	if f := proxyFuncFrom(ProxySetting{}); f != nil {
		t.Fatal("未启用代理时应返回 nil，保证直连")
	}
	f := proxyFuncFrom(ProxySetting{Enabled: true, Server: "127.0.0.1:7890"})
	req, _ := http.NewRequest("GET", "https://api.deepseek.com/v1/chat", nil)
	u, err := f(req)
	if err != nil || u == nil {
		t.Fatalf("应返回代理 URL，实际 %v %v", u, err)
	}
	req, _ = http.NewRequest("GET", "http://localhost:8080/x", nil)
	f = proxyFuncFrom(ProxySetting{Enabled: true, Server: "127.0.0.1:7890", Override: "localhost"})
	if u, _ := f(req); u != nil {
		t.Fatalf("localhost 应绕过代理，实际 %v", u)
	}
}
