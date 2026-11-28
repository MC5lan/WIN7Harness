package remote

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 页面是内联脚本；CSP 必须带 nonce，且注入的 nonce 要与响应头一致，
// 否则现代浏览器会拒绝执行整页 JS（配对/轮询/审批全部失效）。
func TestIndexPageCSPNonce(t *testing.T) {
	s := &Server{}
	h := securityHeaders(http.HandlerFunc(s.handleIndex))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	const marker = "script-src 'nonce-"
	i := strings.Index(csp, marker)
	if i < 0 {
		t.Fatalf("CSP 缺少 script-src nonce：%q", csp)
	}
	rest := csp[i+len(marker):]
	j := strings.IndexByte(rest, '\'')
	if j <= 0 {
		t.Fatalf("CSP nonce 格式异常：%q", csp)
	}
	nonce := rest[:j]

	body := rec.Body.String()
	if !strings.Contains(body, `<script nonce="`+nonce+`">`) {
		t.Fatalf("页面脚本没有注入请求 nonce；body=%q", body)
	}
	if !strings.Contains(body, "harness_token") {
		t.Fatal("页面内容不完整")
	}
}

func TestIndexOnlyRoot(t *testing.T) {
	s := &Server{}
	h := securityHeaders(http.HandlerFunc(s.handleIndex))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("/app.js 应 404，实际 %d", rec.Code)
	}
}
