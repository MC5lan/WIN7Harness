package core

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ExplainNetErr 把网络错误翻译成"卡在哪一层、下一步该干什么"。
// 原始错误里有大量 syscall 文案，用户看不懂，模型也容易据此瞎猜。
func ExplainNetErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	low := strings.ToLower(msg)

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "域名解析失败（" + dnsErr.Name + "）：DNS 不可用或域名写错。可换 DNS（如 223.5.5.5）或用 /netdiag 看解析阶段。"
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return "证书校验失败：常见于系统根证书缺失/过期（Win7 缺 KB4474419）、企业代理自签网关。可在设置里补充根证书文件，或临时用不校验模式排查。"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		// Windows 的文案是 "connectex: No connection could be made because the target
		// machine actively refused it"，没有 "refused" 也有拒绝语义，必须一起认。
		if strings.Contains(low, "refused") || strings.Contains(low, "no connection could be made") ||
			strings.Contains(low, "actively refused") {
			if looksLikeLocalProxy(opErr.Addr) {
				return "连不上本机代理（" + opErr.Addr.String() + "）：代理软件没启动、端口改过，或已被系统防火墙拦下。" +
					"要么启动代理，要么在设置里把代理改成直连。"
			}
			return "连接被拒绝：目标端口没在监听，或本机防火墙拦截（代理软件未启动时最常见）。"
		}
		if strings.Contains(low, "timeout") || strings.Contains(low, "timed out") {
			return "连接超时：网络不通或被墙。检查是否需要代理。"
		}
		if strings.Contains(low, "unreachable") {
			return "网络不可达：本机没有可用出口（断网/VPN 掉线）。"
		}
	}
	switch {
	case strings.Contains(low, "x509"):
		return "证书链有问题（x509）：根证书缺失、证书过期或域名不匹配，详见 /netdiag。"
	case strings.Contains(low, "certificate"):
		return "TLS 证书校验未通过：根证书缺失或代理做了中间人签名。"
	case strings.Contains(low, "tls handshake"):
		return "TLS 握手失败：协议版本被拒（Win7 默认不含 TLS1.2 之外的新套件）或中间设备干扰。"
	case strings.Contains(low, "proxyconnect"):
		return "连代理失败：代理地址/端口不对，或代理需要认证。"
	case strings.Contains(low, "connectex") || strings.Contains(low, "no connection could be made"):
		return "连接被对方拒绝（Windows: connectex）：若地址是本机端口，多半是代理软件没启动或端口不对；" +
			"若地址是远端，则是目标端口没在监听或被防火墙拦下。可用 /netdiag 逐层确认。"
	case strings.Contains(low, "eof"):
		return "对方直接断开连接：可能被防火墙或网关重置。"
	case strings.Contains(low, "no such host"):
		return "域名不存在：检查 API 地址拼写。"
	}
	if i := strings.IndexAny(msg, "\r\n"); i > 0 {
		msg = msg[:i]
	}
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return msg
}

// looksLikeLocalProxy 判断对端是否像本机代理端口（127.0.0.1:7890 这类）。
// 本地端口拒绝十有八九是代理没开，比"目标端口没监听"的说法更有用。
func looksLikeLocalProxy(addr net.Addr) bool {
	if addr == nil {
		return false
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	if host != "127.0.0.1" && host != "::1" && host != "localhost" {
		return false
	}
	// 常见代理端口；不确定时也算本地代理，因为直连本机服务极少见。
	switch {
	case strings.HasSuffix(addr.String(), ":7890"),
		strings.HasSuffix(addr.String(), ":7897"),
		strings.HasSuffix(addr.String(), ":1080"),
		strings.HasSuffix(addr.String(), ":8080"),
		strings.HasSuffix(addr.String(), ":10809"):
		return true
	}
	return true
}

// NetDiagResult 一次分阶段诊断的结果。
type NetDiagResult struct {
	Target  string
	Proxy   string
	Steps   []DiagStep
	Verdict string
	Advice  string
	TotalMs int64
}

// DiagStep 单个阶段的结论。
type DiagStep struct {
	Name string
	OK   bool
	Ms   int64
	Note string
}

func (r NetDiagResult) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "诊断目标：%s\n", r.Target)
	if r.Proxy != "" {
		fmt.Fprintf(&b, "代理：%s\n", r.Proxy)
	} else {
		b.WriteString("代理：直连（未配置代理）\n")
	}
	b.WriteString("\n")
	for _, s := range r.Steps {
		mark := "OK  "
		if !s.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(&b, "[%s] %-10s %5dms  %s\n", mark, s.Name, s.Ms, s.Note)
		if !s.OK {
			break // 前一阶段失败，后面的阶段没有意义
		}
	}
	if r.Verdict != "" {
		fmt.Fprintf(&b, "\n结论：%s\n", r.Verdict)
	}
	if r.Advice != "" {
		fmt.Fprintf(&b, "建议：%s\n", r.Advice)
	}
	fmt.Fprintf(&b, "总耗时：%dms\n", r.TotalMs)
	return b.String()
}

// Diagnose 逐阶段探测：DNS → TCP → TLS → HTTP。
// 目的是把"连不上"拆成具体一层，而不是只丢一个 err。
func Diagnose(rawURL, proxy string, timeout time.Duration) NetDiagResult {
	res := NetDiagResult{Target: rawURL, Proxy: proxy}
	started := time.Now()
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		res.Verdict = "URL 不合法：" + rawURL
		res.TotalMs = time.Since(started).Milliseconds()
		return res
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	// 1. DNS
	t0 := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	addrs, derr := net.DefaultResolver.LookupHost(ctx, host)
	cancel()
	dnsMs := time.Since(t0).Milliseconds()
	if derr != nil {
		res.Steps = append(res.Steps, DiagStep{"DNS", false, dnsMs, ExplainNetErr(derr)})
		res.Verdict = "DNS 解析失败，请求连域名都没出去"
		res.Advice = "换公共 DNS（223.5.5.5 / 119.29.29.29），或确认域名拼写；公司内网域名需要连 VPN。"
		res.TotalMs = time.Since(started).Milliseconds()
		return res
	}
	res.Steps = append(res.Steps, DiagStep{"DNS", true, dnsMs, "解析到 " + strings.Join(trimList(addrs, 2), ", ")})

	// 2. TCP（走代理时由代理负责，用户机器直连探测会误报，所以代理场景跳过并说明）
	if proxy == "" {
		t1 := time.Now()
		conn, cerr := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
		tcpMs := time.Since(t1).Milliseconds()
		if cerr != nil {
			res.Steps = append(res.Steps, DiagStep{"TCP", false, tcpMs, ExplainNetErr(cerr)})
			res.Verdict = fmt.Sprintf("TCP 连不上 %s:%s，说明网络层不通", host, port)
			res.Advice = "检查是否需要代理（公司网络/墙）、目标端口是否正确、本机防火墙是否放行。"
			res.TotalMs = time.Since(started).Milliseconds()
			return res
		}
		conn.Close()
		res.Steps = append(res.Steps, DiagStep{"TCP", true, tcpMs, "已连通 " + host + ":" + port})
	} else {
		res.Steps = append(res.Steps, DiagStep{"TCP", true, 0, "经代理，跳过直连探测"})
	}

	// 3. HTTP（含 TLS 握手），用真实请求验证端到端。
	tr := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: timeout}).DialContext,
		TLSHandshakeTimeout: timeout,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if proxy != "" {
		if pu, perr := url.Parse(proxy); perr == nil {
			tr.Proxy = http.ProxyURL(pu)
			res.Steps = append(res.Steps, DiagStep{"PROXY", true, 0, "使用 " + pu.Redacted()})
		} else {
			res.Steps = append(res.Steps, DiagStep{"PROXY", false, 0, "代理地址不合法：" + proxy})
			res.Verdict = "代理配置写错了"
			res.Advice = "格式形如 http://127.0.0.1:7890"
			res.TotalMs = time.Since(started).Milliseconds()
			return res
		}
	} else {
		tr.Proxy = nil
	}
	client := &http.Client{Transport: tr, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t2 := time.Now()
	req, _ := http.NewRequest("GET", rawURL, nil)
	req.Header.Set("User-Agent", "WIN7Harness-netdiag/1.0")
	resp, herr := client.Do(req)
	httpMs := time.Since(t2).Milliseconds()
	if herr != nil {
		res.Steps = append(res.Steps, DiagStep{"HTTPS", false, httpMs, ExplainNetErr(herr)})
		res.Verdict = "TCP 通了但 HTTPS 请求失败，问题在 TLS 或 HTTP 层"
		res.Advice = "拿到证书报错就补根证书（Win7 缺 KB4474419 是常见原因）；握手失败多为代理中间人或协议版本限制。"
		res.TotalMs = time.Since(started).Milliseconds()
		return res
	}
	resp.Body.Close()
	if u.Scheme == "https" {
		note := fmt.Sprintf("TLS 握手成功，HTTP %d", resp.StatusCode)
		if resp.TLS != nil {
			note += "，协议 " + tlsVersion(resp.TLS.Version)
		}
		res.Steps = append(res.Steps, DiagStep{"HTTPS", true, httpMs, note})
	} else {
		res.Steps = append(res.Steps, DiagStep{"HTTP", true, httpMs, fmt.Sprintf("HTTP %d", resp.StatusCode)})
	}
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		res.Verdict = fmt.Sprintf("网络与 TLS 都正常，但被服务端拒绝（%d）", resp.StatusCode)
		res.Advice = "多半是 API Key / Token 不对或没带鉴权头，不是网络问题。"
	case resp.StatusCode >= 500:
		res.Verdict = fmt.Sprintf("网络通，服务端自己出错（%d）", resp.StatusCode)
		res.Advice = "换时间重试或换节点；反复 5xx 通常是对方故障。"
	default:
		res.Verdict = "链路完全正常"
	}
	res.TotalMs = time.Since(started).Milliseconds()
	return res
}

func trimList(in []string, n int) []string {
	if len(in) > n {
		return in[:n]
	}
	return in
}

func tlsVersion(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS1.0"
	case tls.VersionTLS11:
		return "TLS1.1"
	case tls.VersionTLS12:
		return "TLS1.2"
	case tls.VersionTLS13:
		return "TLS1.3"
	}
	return fmt.Sprintf("0x%x", v)
}

// ProxyFromConfig 把配置里的代理字符串转成 Transport 可用的 Proxy 函数。
// 空串表示不干预（沿用系统/环境变量代理）。
func ProxyFromConfig(proxy string) func(*http.Request) (*url.URL, error) {
	s := strings.TrimSpace(proxy)
	if s == "" {
		return nil
	}
	if s == "direct" || s == "none" || s == "off" {
		return func(*http.Request) (*url.URL, error) { return nil, nil }
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		fmt.Fprintf(os.Stderr, "代理地址不合法，已忽略：%s\n", s)
		return nil
	}
	return http.ProxyURL(u)
}
