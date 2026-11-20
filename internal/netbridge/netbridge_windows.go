//go:build windows

package netbridge

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alexbrainman/sspi"
	"github.com/alexbrainman/sspi/negotiate"
	"github.com/alexbrainman/sspi/ntlm"
	xproxy "golang.org/x/net/proxy"

	"win7harness/internal/core"
)

// 本机 HTTP 转发桥。
//
// 为什么需要：环境变量只能告诉 git/go 代理地址，无法完成 NTLM/Kerberos 的
// 407 握手；Go 的 net/http 也不会自动使用当前 Windows 用户凭据。
// 这里在 127.0.0.1 起一个小代理，子进程只认本机代理；桥接层用 Windows
// SSPI（NTLM，当前登录用户，无需密码）完成上游代理认证，再转发
// CONNECT/HTTP。PAC 每目标解析也在这里做，因此不会出现“一个全局代理
// 覆盖所有域名”的问题。
//
// 只监听 loopback，不暴露局域网；生命周期跟随进程。

type Server struct {
	URL      string
	resolver ProxyResolver
	ln       net.Listener
	stop     chan struct{}

	credOnce sync.Once
	cred     *sspi.Credentials
	credErr  error

	negOnce sync.Once
	negCred *sspi.Credentials
	negErr  error
}

var (
	mu     sync.Mutex
	active *Server
)

// Ensure 启动（或复用）本机代理桥。
func Ensure(resolver ProxyResolver) (*Server, error) {
	mu.Lock()
	defer mu.Unlock()
	if active != nil {
		return active, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{
		URL:      "http://" + ln.Addr().String(),
		resolver: resolver,
		ln:       ln,
		stop:     make(chan struct{}),
	}
	active = s
	go s.acceptLoop()
	core.Logf("netbridge: 本机代理桥已启动 %s（NTLM/Kerberos 使用当前 Windows 用户）", s.URL)
	return s, nil
}

func Current() *Server {
	mu.Lock()
	defer mu.Unlock()
	return active
}

func Stop() {
	mu.Lock()
	s := active
	active = nil
	mu.Unlock()
	if s == nil {
		return
	}
	close(s.stop)
	_ = s.ln.Close()
	core.Logf("netbridge: 本机代理桥已停止")
}

func (s *Server) acceptLoop() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.stop:
				return
			default:
				continue
			}
		}
		go s.serve(c)
	}
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(60 * time.Second))
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method == http.MethodConnect {
		s.handleConnect(c, br, req)
		return
	}
	s.handleHTTP(c, br, req)
}

func (s *Server) handleConnect(c net.Conn, br *bufio.Reader, req *http.Request) {
	target := req.Host
	if target == "" && req.URL != nil {
		target = req.URL.Host
	}
	if target == "" {
		writeErr(c, http.StatusBadRequest, "CONNECT 缺少目标")
		return
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(target, "443")
	}
	targetURL := &url.URL{Scheme: "https", Host: target}

	var upstream *url.URL
	if s.resolver != nil {
		var err error
		upstream, err = s.resolver(targetURL)
		if err != nil {
			writeErr(c, http.StatusBadGateway, "代理解析失败")
			return
		}
	}
	if upstream == nil || isSocksProxy(upstream) {
		var dst net.Conn
		var err error
		if upstream == nil {
			dst, err = net.DialTimeout("tcp", target, 20*time.Second)
		} else {
			dst, err = dialSocks(upstream, target)
		}
		if err != nil {
			writeErr(c, http.StatusBadGateway, "目标连接失败")
			return
		}
		_, _ = io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
		tunnel(c, br, dst, bufio.NewReader(dst))
		return
	}

	upc, upReader, err := s.connectViaProxy(upstream, target)
	if err != nil {
		writeErr(c, http.StatusBadGateway, "上游代理连接/认证失败")
		core.Logf("netbridge: CONNECT %s 失败：%v", target, err)
		return
	}
	_, _ = io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
	tunnel(c, br, upc, upReader)
}

// connectViaProxy 与上游代理完成 CONNECT + NTLM/Kerberos 握手。
func (s *Server) connectViaProxy(upstream *url.URL, target string) (net.Conn, *bufio.Reader, error) {
	upc, err := dialProxy(upstream)
	if err != nil {
		return nil, nil, err
	}
	ur := bufio.NewReader(upc)
	auth := basicAuth(upstream)
	var authState *proxyAuth
	defer func() {
		if authState != nil {
			authState.release()
		}
	}()

	for attempt := 0; attempt < 5; attempt++ {
		if err := writeConnect(upc, target, auth); err != nil {
			upc.Close()
			return nil, nil, err
		}
		resp, err := http.ReadResponse(ur, &http.Request{Method: http.MethodConnect})
		if err != nil {
			upc.Close()
			return nil, nil, err
		}
		if resp.StatusCode == http.StatusOK {
			// 200 的 Body 就是隧道数据源；不要 Close，交给 tunnel 用 ur。
			return upc, ur, nil
		}
		if resp.StatusCode != http.StatusProxyAuthRequired {
			resp.Body.Close()
			upc.Close()
			return nil, nil, fmt.Errorf("代理返回 HTTP %d", resp.StatusCode)
		}
		scheme, challenge := proxyChallenge(resp)
		resp.Body.Close()
		if authState == nil {
			authState, auth, err = s.newProxyAuth(scheme, upstream.Hostname())
			if err != nil {
				upc.Close()
				return nil, nil, err
			}
			continue
		}
		if len(challenge) == 0 {
			if authState.canFallback() {
				authState.release()
				authState, auth, err = s.newProxyAuth("NTLM", upstream.Hostname())
				if err != nil {
					upc.Close()
					return nil, nil, err
				}
				continue
			}
			upc.Close()
			return nil, nil, fmt.Errorf("代理要求 %s 但没有提供 challenge", scheme)
		}
		auth, err = authState.update(challenge)
		if err != nil {
			if authState.canFallback() {
				authState.release()
				authState, auth, err = s.newProxyAuth("NTLM", upstream.Hostname())
				if err == nil {
					continue
				}
			}
			upc.Close()
			return nil, nil, err
		}
	}
	upc.Close()
	return nil, nil, fmt.Errorf("代理认证重试次数过多")
}

func (s *Server) handleHTTP(c net.Conn, br *bufio.Reader, req *http.Request) {
	if req.URL == nil || req.URL.Host == "" {
		writeErr(c, http.StatusBadRequest, "只接受代理形式的绝对 URL")
		return
	}
	const maxBody = 64 << 20
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(io.LimitReader(req.Body, maxBody+1))
		req.Body.Close()
		if err != nil {
			writeErr(c, http.StatusBadGateway, "读取请求体失败")
			return
		}
		if len(body) > maxBody {
			writeErr(c, http.StatusRequestEntityTooLarge, "请求体超过桥接上限")
			return
		}
	}

	var upstream *url.URL
	if s.resolver != nil {
		var err error
		upstream, err = s.resolver(req.URL)
		if err != nil {
			writeErr(c, http.StatusBadGateway, "代理解析失败")
			return
		}
	}
	dialAddr := req.URL.Host
	if upstream != nil && !isSocksProxy(upstream) {
		if _, _, err := net.SplitHostPort(upstream.Host); err != nil {
			dialAddr = net.JoinHostPort(upstream.Hostname(), "80")
		} else {
			dialAddr = upstream.Host
		}
	}
	var conn net.Conn
	var err error
	if upstream != nil && isSocksProxy(upstream) {
		conn, err = dialSocks(upstream, req.URL.Host)
	} else {
		conn, err = net.DialTimeout("tcp", dialAddr, 20*time.Second)
	}
	if err != nil {
		writeErr(c, http.StatusBadGateway, "目标/代理连接失败")
		return
	}
	defer conn.Close()
	rb := bufio.NewReader(conn)
	auth := ""
	if upstream != nil && !isSocksProxy(upstream) {
		auth = basicAuth(upstream)
	}
	var authState *proxyAuth
	defer func() {
		if authState != nil {
			authState.release()
		}
	}()

	for attempt := 0; attempt < 5; attempt++ {
		req2 := req.Clone(req.Context())
		req2.RequestURI = ""
		req2.Body = io.NopCloser(bytes.NewReader(body))
		req2.ContentLength = int64(len(body))
		req2.Header.Del("Proxy-Authorization")
		if auth != "" {
			req2.Header.Set("Proxy-Authorization", auth)
		}
		if upstream != nil && !isSocksProxy(upstream) {
			if err := req2.WriteProxy(conn); err != nil {
				return
			}
		} else {
			if err := req2.Write(conn); err != nil {
				return
			}
		}
		resp, err := http.ReadResponse(rb, req2)
		if err != nil {
			writeErr(c, http.StatusBadGateway, "读取上游响应失败")
			return
		}
		if resp.StatusCode != http.StatusProxyAuthRequired {
			if err := resp.Write(c); err != nil {
				resp.Body.Close()
				return
			}
			resp.Body.Close()
			return
		}
		scheme, challenge := proxyChallenge(resp)
		resp.Body.Close()
		if authState == nil {
			authState, auth, err = s.newProxyAuth(scheme, upstream.Hostname())
			if err != nil {
				writeErr(c, http.StatusBadGateway, "代理认证初始化失败")
				return
			}
			continue
		}
		if len(challenge) == 0 {
			if authState.canFallback() {
				authState.release()
				authState, auth, err = s.newProxyAuth("NTLM", upstream.Hostname())
				if err != nil {
					writeErr(c, http.StatusBadGateway, "NTLM 初始化失败")
					return
				}
				continue
			}
			writeErr(c, http.StatusBadGateway, "代理未提供认证 challenge")
			return
		}
		auth, err = authState.update(challenge)
		if err != nil {
			if authState.canFallback() {
				authState.release()
				authState, auth, err = s.newProxyAuth("NTLM", upstream.Hostname())
				if err == nil {
					continue
				}
			}
			writeErr(c, http.StatusBadGateway, "代理认证失败")
			return
		}
	}
	writeErr(c, http.StatusBadGateway, "代理认证重试次数过多")
}

func (s *Server) credentials() (*sspi.Credentials, error) {
	s.credOnce.Do(func() {
		s.cred, s.credErr = ntlm.AcquireCurrentUserCredentials()
	})
	return s.cred, s.credErr
}

func (s *Server) negotiateCredentials() (*sspi.Credentials, error) {
	s.negOnce.Do(func() {
		s.negCred, s.negErr = negotiate.AcquireCurrentUserCredentials()
	})
	return s.negCred, s.negErr
}

// proxyAuth 封装一次代理认证握手。优先 Negotiate（Kerberos，失败可回退
// NTLM），NTLM 上下文与 Negotiate 上下文互斥。
type proxyAuth struct {
	scheme string
	host   string
	ntlm   *ntlm.ClientContext
	neg    *negotiate.ClientContext
}

func (a *proxyAuth) release() {
	if a == nil {
		return
	}
	if a.ntlm != nil {
		_ = a.ntlm.Release()
		a.ntlm = nil
	}
	if a.neg != nil {
		_ = a.neg.Release()
		a.neg = nil
	}
}

func (a *proxyAuth) canFallback() bool { return a != nil && a.neg != nil }

func (a *proxyAuth) update(challenge []byte) (string, error) {
	if a == nil {
		return "", fmt.Errorf("代理认证上下文为空")
	}
	switch {
	case a.neg != nil:
		_, token, err := a.neg.Update(challenge)
		if err != nil {
			return "", err
		}
		return a.scheme + " " + base64.StdEncoding.EncodeToString(token), nil
	case a.ntlm != nil:
		token, err := a.ntlm.Update(challenge)
		if err != nil {
			return "", err
		}
		return a.scheme + " " + base64.StdEncoding.EncodeToString(token), nil
	default:
		return "", fmt.Errorf("代理认证上下文未初始化")
	}
}

func (s *Server) newProxyAuth(scheme, host string) (*proxyAuth, string, error) {
	if strings.EqualFold(strings.TrimSpace(scheme), "Negotiate") {
		cred, err := s.negotiateCredentials()
		if err == nil {
			spn := "HTTP/" + strings.TrimSpace(host)
			ctx, token, nerr := negotiate.NewClientContext(cred, spn)
			if nerr == nil {
				return &proxyAuth{scheme: "Negotiate", host: host, neg: ctx},
					"Negotiate " + base64.StdEncoding.EncodeToString(token), nil
			}
			core.Logf("netbridge: Negotiate(%s) 初始化失败，回退 NTLM：%v", spn, nerr)
		} else {
			core.Logf("netbridge: Negotiate 凭据不可用，回退 NTLM：%v", err)
		}
	}
	cred, err := s.credentials()
	if err != nil {
		return nil, "", err
	}
	ctx, token, err := ntlm.NewClientContext(cred)
	if err != nil {
		return nil, "", err
	}
	return &proxyAuth{scheme: "NTLM", host: host, ntlm: ctx},
		"NTLM " + base64.StdEncoding.EncodeToString(token), nil
}

func isSocksProxy(u *url.URL) bool {
	if u == nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "socks", "socks5", "socks5h":
		return true
	}
	return false
}

func dialSocks(up *url.URL, target string) (net.Conn, error) {
	host := up.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(up.Hostname(), "1080")
	}
	var auth *xproxy.Auth
	if up.User != nil {
		pass, _ := up.User.Password()
		auth = &xproxy.Auth{User: up.User.Username(), Password: pass}
	}
	d, err := xproxy.SOCKS5("tcp", host, auth, &net.Dialer{Timeout: 20 * time.Second})
	if err != nil {
		return nil, err
	}
	return d.Dial("tcp", target)
}

func dialProxy(up *url.URL) (net.Conn, error) {
	host := up.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(up.Hostname(), "80")
	}
	return net.DialTimeout("tcp", host, 20*time.Second)
}

func writeConnect(w io.Writer, target, auth string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Connection: Keep-Alive\r\n", target, target)
	if auth != "" {
		fmt.Fprintf(&b, "Proxy-Authorization: %s\r\n", auth)
	}
	b.WriteString("\r\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func basicAuth(up *url.URL) string {
	if up == nil || up.User == nil {
		return ""
	}
	user := up.User.Username()
	pass, _ := up.User.Password()
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func proxyChallenge(resp *http.Response) (scheme string, challenge []byte) {
	// 优先 Negotiate：Windows SSPI 会在 Kerberos 不可用时自动回退 NTLM。
	// 某些代理同时发 NTLM 和 Negotiate，不能让 header 顺序决定用哪个。
	var ntlmRaw []byte
	ntlmSeen := false
	for _, h := range resp.Header.Values("Proxy-Authenticate") {
		f := strings.Fields(h)
		if len(f) == 0 {
			continue
		}
		s := f[0]
		up := strings.ToUpper(s)
		raw := []byte{}
		if len(f) > 1 {
			raw, _ = base64.StdEncoding.DecodeString(f[1])
		}
		switch up {
		case "NEGOTIATE":
			return s, raw
		case "NTLM":
			if !ntlmSeen {
				ntlmSeen = true
				ntlmRaw = raw
			}
		}
	}
	if ntlmSeen {
		return "NTLM", ntlmRaw
	}
	return "NTLM", nil
}

func tunnel(client net.Conn, clientReader *bufio.Reader, server net.Conn, serverReader *bufio.Reader) {
	_ = client.SetDeadline(time.Time{})
	_ = server.SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(server, clientReader)
		if t, ok := server.(*net.TCPConn); ok {
			_ = t.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, serverReader)
		if t, ok := client.(*net.TCPConn); ok {
			_ = t.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
	<-done
}

func writeErr(c net.Conn, code int, msg string) {
	body := msg + "\n"
	fmt.Fprintf(c, "HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		code, http.StatusText(code), len(body), body)
}
