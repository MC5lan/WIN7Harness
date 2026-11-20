package tools

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/llm"
	"win7harness/internal/netbridge"
)

func init() {
	register(Tool{
		Name: "web_fetch",
		Desc: "抓取一个网页并转换成可读正文（去脚本样式、解实体、压空白），用于查文档、看报错、读 changelog。" +
			"只支持 http/https 公网地址，内网与回环地址会被拒绝；默认返回前 8000 字，可调大到 40000。" +
			"纯静态抓取，不执行 JavaScript，需要登录或点击的页面请改用浏览器。",
		Schema: `{
  "type":"object",
  "properties":{
    "url":{"type":"string","description":"要抓取的网页地址，必须 http:// 或 https:// 开头"},
    "max_chars":{"type":"integer","description":"正文最多返回多少字，默认 8000，最大 40000"},
    "raw":{"type":"boolean","description":"为 true 时返回原始 HTML 而不是提取正文（调试用）"}
  },
  "required":["url"]
}`,
		Run: runWebFetch,
	})
}

const (
	webFetchBodyLimit = 2 << 20 // 2MB：再大基本不是文章
	webFetchDefault   = 8000
	webFetchMaxChars  = 40000
)

func runWebFetch(ctx *Context, args map[string]interface{}) (Result, error) {
	raw := strings.TrimSpace(Get(args, "url"))
	if raw == "" {
		return Result{}, fmt.Errorf("url 为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Result{}, fmt.Errorf("地址不合法: %v", err)
	}
	if err := checkPublicURL(u); err != nil {
		return Result{}, err
	}

	maxChars := GetInt(args, "max_chars", webFetchDefault)
	if maxChars <= 0 {
		maxChars = webFetchDefault
	}
	if maxChars > webFetchMaxChars {
		maxChars = webFetchMaxChars
	}

	client := &http.Client{
		Timeout:   25 * time.Second,
		Transport: safeTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("重定向超过 5 次")
			}
			return checkPublicURL(req.URL)
		},
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 6.1; Win64; x64) Win7Harness/1.22")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json;q=0.9,*/*;q=0.5")

	if ctx.Progress != nil {
		ctx.Progress("GET " + u.String())
	}
	resp, err := client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		hint := ""
		switch resp.StatusCode {
		case 401, 403:
			hint = "\n提示：需要登录或该站点拒绝抓取，改用浏览器工具。"
		case 404:
			hint = "\n提示：地址不存在，检查路径拼写。"
		case 429:
			hint = "\n提示：被限流，稍后再试。"
		}
		return Result{}, fmt.Errorf("HTTP %s%s\n%s", resp.Status, hint, truncateRunes(stripTags(string(body)), 300))
	}

	body, truncated, err := readLimited(resp.Body, webFetchBodyLimit)
	if err != nil {
		return Result{}, fmt.Errorf("读取响应失败: %v", err)
	}

	ctype := resp.Header.Get("Content-Type")
	charset := charsetOf(ctype, body)
	text := string(body)
	title := ""
	if GetBool(args, "raw", false) {
		// 原样返回，仅截断
	} else if isTextual(ctype) {
		title = pageTitle(text)
		if strings.Contains(ctype, "html") || looksLikeHTML(text) {
			text = htmlToText(text)
		} else {
			text = normalizeSpace(text)
		}
	} else {
		return Result{
			Output: fmt.Sprintf("URL: %s\n状态: %s\n类型: %s\n大小: %s\n\n该类型不是文本，未提取正文。"+
				"如需内容请换成网页地址，或让用户提供文件。", u.String(), resp.Status, ctype, core.HumanSize(int64(len(body)))),
		}, nil
	}

	if charset != "" && !isUTF8Compatible(charset) {
		text = "（注意：该页面声明编码 " + charset + "，本工具只按 UTF-8 解码，正文可能有乱码）\n" + text
	}

	full := len([]rune(text))
	text = truncateRunes(text, maxChars)

	var b strings.Builder
	fmt.Fprintf(&b, "URL: %s\n", resp.Request.URL.String())
	fmt.Fprintf(&b, "状态: %s\n", resp.Status)
	if ctype != "" {
		fmt.Fprintf(&b, "类型: %s\n", ctype)
	}
	fmt.Fprintf(&b, "大小: %s", core.HumanSize(int64(len(body))))
	if truncated {
		b.WriteString("（超过 2MB 已截断）")
	}
	b.WriteString("\n")
	if title != "" {
		fmt.Fprintf(&b, "标题: %s\n", title)
	}
	fmt.Fprintf(&b, "字数: %d", full)
	if full > maxChars {
		fmt.Fprintf(&b, "（只保留前 %d 字）", maxChars)
	}
	b.WriteString("\n")
	b.WriteString("---\n")
	b.WriteString(text)
	return Result{Output: b.String(), Title: "网页"}, nil
}

// safeTransport 建立连接时再校验一次 IP，避免 DNS 重新解析指向内网（DNS rebinding）。
var (
	transportOnce sync.Once
	transportPool *http.Transport

	// envProxyHosts：HTTP_PROXY/HTTPS_PROXY/ALL_PROXY 里显式配置的代理主机。
	// 这类代理是用户自己选的（公司内网代理、本地 Clash/V2Ray），
	// DialContext 不该把它当成"内网地址"拒掉——否则配了代理的机器
	// 全部 web_fetch 失败。
	envProxyHosts = map[string]bool{}
)

func init() {
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			if u, err := url.Parse(v); err == nil && u != nil {
				if h := strings.ToLower(u.Hostname()); h != "" {
					envProxyHosts[h] = true
				}
			}
		}
	}
}

func safeTransport() *http.Transport {
	transportOnce.Do(func() {
		dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
		proxyFunc := http.ProxyFromEnvironment
		if llm.SystemProxyConfigured() {
			if s, err := netbridge.Ensure(llm.ResolveProxyForURL); err == nil && s != nil {
				if pu, perr := url.Parse(s.URL); perr == nil {
					envProxyHosts[strings.ToLower(pu.Hostname())] = true
					proxyFunc = http.ProxyURL(pu)
				}
			}
		}
		transportPool = &http.Transport{
			Proxy: proxyFunc,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				// 环境变量显式指定的代理：直接放行（代理本身由用户配置）
				if envProxyHosts[strings.ToLower(host)] {
					return dialer.DialContext(ctx, network, addr)
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				for _, ip := range ips {
					if !isPublicIP(ip.IP) {
						return nil, fmt.Errorf("拒绝连接内网地址 %s（解析为 %s）", host, ip.IP)
					}
				}
				// 逐个尝试已校验的 IP，避免二次解析
				var lastErr error
				for _, ip := range ips {
					conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
					if err == nil {
						return conn, nil
					}
					lastErr = err
				}
				if lastErr == nil {
					lastErr = fmt.Errorf("无法连接 %s", host)
				}
				return nil, lastErr
			},
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			MaxIdleConns:          16,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       90 * time.Second,
			DisableCompression:    false,
		}
	})
	return transportPool
}

// checkPublicURL 拦截非 http(s) 与非公网目标。
func checkPublicURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("只支持 http / https 地址")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("地址缺少主机名")
	}
	lower := strings.ToLower(host)
	for _, suffix := range []string{
		".localhost", ".local", ".internal",
		".lan", ".corp", ".intranet", ".home.arpa",
	} {
		if strings.HasSuffix(lower, suffix) {
			return fmt.Errorf("拒绝访问本机或内网域名：%s", host)
		}
	}
	if lower == "localhost" {
		return fmt.Errorf("拒绝访问本机或内网域名：%s", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("拒绝访问内网地址：%s", host)
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("域名解析失败：%v", err)
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return fmt.Errorf("域名 %s 指向内网地址 %s，已拒绝", host, ip)
		}
	}
	return nil
}

// isPublicIP 判断是否公网可路由地址。
func isPublicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// 0.0.0.0/8、169.254/16 已由上面覆盖；再挡掉运营商级 NAT 与保留段
		switch {
		case v4[0] == 0, v4[0] == 127:
			return false
		case v4[0] == 169 && v4[1] == 254:
			return false
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127: // 100.64/10 CGNAT
			return false
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 2: // 192.0.2.0/24 TEST-NET-1（RFC5737）
			return false
		case v4[0] == 198 && v4[1] == 51 && v4[2] == 100: // 198.51.100.0/24 TEST-NET-2
			return false
		case v4[0] == 203 && v4[1] == 0 && v4[2] == 113: // 203.0.113.0/24 TEST-NET-3
			return false
		case v4[0] == 198 && v4[1] >= 18 && v4[1] <= 19: // 198.18.0.0/15 基准测试段（RFC2544）
			return false
		case v4[0] >= 240: // 240.0.0.0/4 保留（含 255.255.255.255 广播）
			return false
		}
	}
	return true
}

// readLimited 读取有上限的响应体，返回内容与是否被截断。
func readLimited(r io.Reader, limit int64) ([]byte, bool, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(b)) > limit {
		return b[:limit], true, nil
	}
	return b, false, nil
}

func isTextual(ctype string) bool {
	c := strings.ToLower(ctype)
	if c == "" {
		return true
	}
	for _, k := range []string{"text/", "application/json", "application/xml", "application/xhtml", "application/rss", "application/atom", "javascript"} {
		if strings.Contains(c, k) {
			return true
		}
	}
	return false
}

func charsetOf(ctype string, body []byte) string {
	low := strings.ToLower(ctype)
	if i := strings.Index(low, "charset="); i >= 0 {
		cs := low[i+len("charset="):]
		if j := strings.IndexAny(cs, "; "); j >= 0 {
			cs = cs[:j]
		}
		return strings.Trim(cs, `"'`)
	}
	// 头部没写就看 meta 前 2048 字节
	head := body
	if len(head) > 2048 {
		head = head[:2048]
	}
	if m := reMetaCharset.FindSubmatch(head); m != nil {
		return strings.ToLower(string(m[1]))
	}
	return ""
}

var reMetaCharset = regexp.MustCompile(`(?i)<meta[^>]+charset=["']?\s*([a-z0-9\-_]+)`)

func isUTF8Compatible(cs string) bool {
	switch strings.ToLower(strings.TrimSpace(cs)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return true
	}
	return false
}

func looksLikeHTML(s string) bool {
	low := strings.ToLower(s)
	return strings.Contains(low, "<html") || strings.Contains(low, "<body") || strings.Contains(low, "<div") || strings.Contains(low, "<p>")
}

var (
	reTitle    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	reDrop     = regexp.MustCompile(`(?is)<(script|style|noscript|svg|iframe|template)[^>]*>.*?</\s*(script|style|noscript|svg|iframe|template)\s*>`)
	reTagAny   = regexp.MustCompile(`(?s)<[^>]*>`)
	reNewline  = regexp.MustCompile("[ \\t\\f\\v\\x{00a0}]+")
	reBlankRun = regexp.MustCompile(`\n{3,}`)
	reEntity   = regexp.MustCompile(`&(#x?[0-9a-fA-F]+|[a-zA-Z]+);`)
)

func pageTitle(html string) string {
	if m := reTitle.FindStringSubmatch(html); m != nil {
		return normalizeSpace(decodeEntities(stripTags(m[1])))
	}
	return ""
}

// stripTags 只去标签不解实体，用于标题与错误页预览。
func stripTags(s string) string {
	s = reComment.ReplaceAllString(s, " ")
	s = reDrop.ReplaceAllString(s, " ")
	s = reTagAny.ReplaceAllString(s, " ")
	return normalizeSpace(decodeEntities(s))
}

// 块级标签模式预编译为包级变量：此前每次调用重编译十几条正则。
var (
	reBlockTag = regexp.MustCompile(`(?is)</?\s*(?:p|div|section|article|header|footer|blockquote|pre|table|ul|ol|h[1-6])[^>]*>`)
	reBrTag    = regexp.MustCompile(`(?is)<br\s*/?>`)
	reLiOpen   = regexp.MustCompile(`(?is)<\s*li[^>]*>`)
	reLiClose  = regexp.MustCompile(`(?is)<\s*/\s*li\s*>`)
	reTrTag    = regexp.MustCompile(`(?is)</?\s*tr[^>]*>`)
	reTdOpen   = regexp.MustCompile(`(?is)<\s*(td|th)[^>]*>`)
	reTdClose  = regexp.MustCompile(`(?is)</\s*(td|th)\s*>`)
	reHrTag    = regexp.MustCompile(`(?is)<hr[^>]*>`)
)

// htmlToText 把 HTML 转成可读正文：块级标签换行、列表加前缀、表格单元格分隔。
func htmlToText(html string) string {
	s := html
	s = reComment.ReplaceAllString(s, "")
	s = reDrop.ReplaceAllString(s, " ")
	// 标题与段落：前后留空行
	s = reBlockTag.ReplaceAllString(s, "\n\n")
	s = reBrTag.ReplaceAllString(s, "\n")
	s = reLiOpen.ReplaceAllString(s, "\n- ")
	s = reLiClose.ReplaceAllString(s, "\n")
	s = reTrTag.ReplaceAllString(s, "\n")
	s = reTdOpen.ReplaceAllString(s, "")
	s = reTdClose.ReplaceAllString(s, " | ")
	s = reHrTag.ReplaceAllString(s, "\n---\n")
	s = reTagAny.ReplaceAllString(s, "")
	s = decodeEntities(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	// 行内空白压缩，但保留换行
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		ln = strings.TrimSpace(reNewline.ReplaceAllString(ln, " "))
		ln = strings.TrimSuffix(ln, " |")
		lines[i] = strings.TrimSpace(ln)
	}
	s = strings.Join(lines, "\n")
	s = reBlankRun.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func normalizeSpace(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = reNewline.ReplaceAllString(s, " ")
	s = reBlankRun.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

var namedEntities = map[string]string{
	"amp": "&", "lt": "<", "gt": ">", "quot": `"`, "apos": "'", "nbsp": " ",
	"mdash": "—", "ndash": "–", "hellip": "…", "middot": "·", "copy": "©",
	"reg": "®", "trade": "™", "laquo": "«", "raquo": "»", "times": "×",
	"divide": "÷", "deg": "°", "plusmn": "±", "ldquo": "“", "rdquo": "”",
	"lsquo": "‘", "rsquo": "’", "bull": "•", "dagger": "†", "para": "¶",
}

// decodeEntities 解码常见 HTML 实体与数字实体。
func decodeEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	return reEntity.ReplaceAllStringFunc(s, func(m string) string {
		body := m[1 : len(m)-1]
		if strings.HasPrefix(body, "#") {
			num := body[1:]
			base := 10
			if strings.HasPrefix(num, "x") || strings.HasPrefix(num, "X") {
				base = 16
				num = num[1:]
			}
			n, err := strconv.ParseInt(num, base, 32)
			if err != nil || n <= 0 || n > 0x10FFFF {
				return m
			}
			return string(rune(n))
		}
		if v, ok := namedEntities[strings.ToLower(body)]; ok {
			return v
		}
		return m
	})
}
