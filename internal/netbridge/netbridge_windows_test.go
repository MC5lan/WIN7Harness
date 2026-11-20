//go:build windows

package netbridge

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alexbrainman/sspi"
	"github.com/alexbrainman/sspi/negotiate"
	"github.com/alexbrainman/sspi/ntlm"
)

func proxyClient(t *testing.T, s *Server) *http.Client {
	t.Helper()
	pu, err := url.Parse(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(pu),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
}

func TestBridgeDirectHTTPAndHTTPS(t *testing.T) {
	Stop()
	s, err := Ensure(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer Stop()

	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "plain-ok:"+r.URL.Path)
	}))
	defer plain.Close()
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "tls-ok:"+r.URL.Path)
	}))
	defer tlsSrv.Close()

	c := proxyClient(t, s)
	for _, tc := range []struct {
		url  string
		want string
	}{
		{plain.URL + "/a", "plain-ok:/a"},
		{tlsSrv.URL + "/b", "tls-ok:/b"},
	} {
		resp, err := c.Get(tc.url)
		if err != nil {
			t.Fatalf("%s: %v", tc.url, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !strings.Contains(string(body), tc.want) {
			t.Fatalf("%s: body=%q", tc.url, body)
		}
	}
}

// fakeNTLMProxy 用 Windows SSPI 服务端模拟一个要求 NTLM 的 CONNECT 代理。
func startFakeNTLMProxy(t *testing.T) (string, func()) {
	t.Helper()
	cred, err := ntlm.AcquireServerCredentials()
	if err != nil {
		t.Fatalf("服务端 SSPI 凭据不可用：%v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeProxy(c, cred)
		}
	}()
	return "http://" + ln.Addr().String(), func() { ln.Close() }
}

func serveFakeProxy(c net.Conn, cred *sspi.Credentials) {
	defer c.Close()
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil || req.Method != http.MethodConnect {
		return
	}
	target := req.Host
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(target, "443")
	}
	_ = req.Body.Close()

	// 1) 无认证 -> 要求 NTLM Type1
	writeProxy407(c, "")
	req, err = http.ReadRequest(br)
	if err != nil {
		return
	}
	tok1 := proxyToken(req.Header.Get("Proxy-Authorization"))
	_ = req.Body.Close()
	if len(tok1) == 0 {
		return
	}
	sctx, tok2, err := ntlm.NewServerContext(cred, tok1)
	if err != nil {
		return
	}
	defer sctx.Release()
	writeProxy407(c, base64.StdEncoding.EncodeToString(tok2))

	// 2) Type3 -> 认证完成，建立到目标的隧道
	req, err = http.ReadRequest(br)
	if err != nil {
		return
	}
	tok3 := proxyToken(req.Header.Get("Proxy-Authorization"))
	_ = req.Body.Close()
	if len(tok3) == 0 || sctx.Update(tok3) != nil {
		return
	}
	dst, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		return
	}
	defer dst.Close()
	_, _ = io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
	go func() { _, _ = io.Copy(dst, br) }()
	_, _ = io.Copy(c, dst)
}

func writeProxy407(c net.Conn, token string) {
	writeProxy407Scheme(c, "NTLM", token)
}

func writeProxy407Scheme(c net.Conn, scheme, token string) {
	if token == "" {
		_, _ = fmt.Fprintf(c, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: %s\r\nContent-Length: 0\r\nConnection: keep-alive\r\n\r\n", scheme)
		return
	}
	_, _ = fmt.Fprintf(c, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: %s %s\r\nContent-Length: 0\r\nConnection: keep-alive\r\n\r\n", scheme, token)
}

func proxyToken(h string) []byte {
	f := strings.Fields(h)
	if len(f) < 2 {
		return nil
	}
	tok, _ := base64.StdEncoding.DecodeString(f[1])
	return tok
}

func TestBridgeNTLMProxy(t *testing.T) {
	Stop()
	proxyURL, stopProxy := startFakeNTLMProxy(t)
	defer stopProxy()
	pu, _ := url.Parse(proxyURL)
	s, err := Ensure(func(*url.URL) (*url.URL, error) { return pu, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer Stop()

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ntlm-ok")
	}))
	defer tlsSrv.Close()

	resp, err := proxyClient(t, s).Get(tlsSrv.URL)
	if err != nil {
		t.Fatalf("NTLM 代理握手失败：%v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "ntlm-ok") {
		t.Fatalf("body=%q", body)
	}
}

// startFakeNegotiateProxy 模拟只提供 Negotiate 的代理。Windows Negotiate
// 在无 KDC 的测试机上通常会回退 NTLM，但走的是 SPNEGO/SSPI 的同一套代码路径。
func startFakeNegotiateProxy(t *testing.T) (string, func()) {
	t.Helper()
	cred, err := negotiate.AcquireServerCredentials("")
	if err != nil {
		t.Fatalf("服务端 Negotiate 凭据不可用：%v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serveFakeNegotiateProxy(c, cred)
		}
	}()
	return "http://" + ln.Addr().String(), func() { ln.Close() }
}

func serveFakeNegotiateProxy(c net.Conn, cred *sspi.Credentials) {
	defer c.Close()
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil || req.Method != http.MethodConnect {
		return
	}
	target := req.Host
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(target, "443")
	}
	_ = req.Body.Close()

	writeProxy407Scheme(c, "Negotiate", "")
	req, err = http.ReadRequest(br)
	if err != nil {
		return
	}
	tok1 := proxyToken(req.Header.Get("Proxy-Authorization"))
	_ = req.Body.Close()
	sctx, done, tok2, err := negotiate.NewServerContext(cred, tok1)
	if err != nil {
		return
	}
	defer sctx.Release()
	for round := 0; round < 3 && !done; round++ {
		writeProxy407Scheme(c, "Negotiate", base64.StdEncoding.EncodeToString(tok2))
		req, err = http.ReadRequest(br)
		if err != nil {
			return
		}
		tok := proxyToken(req.Header.Get("Proxy-Authorization"))
		_ = req.Body.Close()
		if len(tok) == 0 {
			return
		}
		done, tok2, err = sctx.Update(tok)
		if err != nil {
			return
		}
	}
	if !done {
		return
	}
	dst, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		return
	}
	defer dst.Close()
	_, _ = io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
	go func() { _, _ = io.Copy(dst, br) }()
	_, _ = io.Copy(c, dst)
}

func TestBridgeNegotiateProxy(t *testing.T) {
	Stop()
	proxyURL, stopProxy := startFakeNegotiateProxy(t)
	defer stopProxy()
	pu, _ := url.Parse(proxyURL)
	s, err := Ensure(func(*url.URL) (*url.URL, error) { return pu, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer Stop()

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "negotiate-ok")
	}))
	defer tlsSrv.Close()

	resp, err := proxyClient(t, s).Get(tlsSrv.URL)
	if err != nil {
		t.Fatalf("Negotiate 代理握手失败：%v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "negotiate-ok") {
		t.Fatalf("body=%q", body)
	}
}

// TestLiveProxy 用于真实企业代理现场验收：
//
//	WIN7H_TEST_PROXY=http://proxy.corp.example:8080 go test ./internal/netbridge -run TestLiveProxy -v
//
// 代理需要当前 Windows 登录用户已有权限；NTLM/Negotiate 都不会要求输入密码。
func TestLiveProxy(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("WIN7H_TEST_PROXY"))
	if raw == "" {
		t.Skip("设置 WIN7H_TEST_PROXY 后运行真实代理验收")
	}
	pu, err := url.Parse(raw)
	if err != nil || pu.Host == "" {
		t.Fatalf("WIN7H_TEST_PROXY 不合法：%q", raw)
	}
	Stop()
	s, err := Ensure(func(*url.URL) (*url.URL, error) { return pu, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer Stop()
	c := proxyClient(t, s)
	resp, err := c.Get("https://example.com/")
	if err != nil {
		t.Fatalf("真实代理 TLS/CONNECT 失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("真实代理返回 HTTP %d", resp.StatusCode)
	}
}

func TestCurrentUserNTLMToken(t *testing.T) {
	cred, err := ntlm.AcquireCurrentUserCredentials()
	if err != nil {
		t.Fatalf("当前用户 SSPI 凭据不可用：%v", err)
	}
	defer cred.Release()
	ctx, token, err := ntlm.NewClientContext(cred)
	if err != nil {
		t.Fatalf("NTLM Type1 生成失败：%v", err)
	}
	defer ctx.Release()
	if len(token) == 0 {
		t.Fatal("NTLM Type1 token 为空")
	}
}
