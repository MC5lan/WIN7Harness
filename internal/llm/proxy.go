package llm

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// ProxySetting 是系统（WinINET）代理设置的可移植表示，
// 拆成纯逻辑放在这里，方便在 Linux 上单测解析与绕过规则。
type ProxySetting struct {
	Enabled  bool
	Server   string // "host:port" 或 "http=h:p;https=h:p;socks=h:p"
	Override string // 分号分隔的绕过列表，支持 <local>
}

// isLoopbackHost 判断目标是否本机。代理桥是显式 Proxy（不经过
// ProxyFromEnvironment 的 localhost 自动绕过），所以这里必须自己放行，
// 否则本地模型/本地自检服务会被错误地送到公司上游代理。
func isLoopbackHost(host string) bool {
	h := strings.Trim(host, "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// parseProxyTarget 从 ProxyServer 串里取出指定协议对应的代理地址，
// 并说明它是不是 SOCKS 代理（SOCKS 需要换 scheme 才能被 transport 识别）。
// 支持四种写法：单一 host:port、http=...;https=...、socks=...、以及混写。
func parseProxyTarget(server, scheme string) (addr string, socks bool) {
	server = strings.TrimSpace(server)
	if server == "" {
		return "", false
	}
	if !strings.Contains(server, "=") {
		return server, false
	}
	var httpP, httpsP, socksP string
	for _, part := range strings.Split(server, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "http":
			httpP = strings.TrimSpace(v)
		case "https":
			httpsP = strings.TrimSpace(v)
		case "socks", "socks5":
			socksP = strings.TrimSpace(v)
		}
	}
	switch strings.ToLower(scheme) {
	case "https":
		if httpsP != "" {
			return httpsP, false
		}
	case "http":
		if httpP != "" {
			return httpP, false
		}
		if httpsP != "" {
			return httpsP, false
		}
	}
	if httpP != "" && socksP == "" {
		return httpP, false
	}
	if socksP != "" {
		return socksP, true
	}
	return "", false
}

// parseProxyServer 只要地址，供展示与单测使用。
func parseProxyServer(server, scheme string) string {
	addr, _ := parseProxyTarget(server, scheme)
	return addr
}

// proxyURLFor 把系统代理设置拼成 transport 认识的 URL。
func proxyURLFor(set ProxySetting, scheme string) *url.URL {
	if !set.Enabled {
		return nil
	}
	addr, socks := parseProxyTarget(set.Server, scheme)
	if addr == "" {
		return nil
	}
	if !strings.Contains(addr, "://") {
		if socks || strings.HasPrefix(addr, "socks") {
			addr = "socks5://" + strings.TrimPrefix(strings.TrimPrefix(addr, "socks5://"), "socks://")
		} else {
			addr = "http://" + addr
		}
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return nil
	}
	return u
}

// bypassHost 判断主机是否命中绕过列表。空列表不绕过。
// "<local>" 表示所有不含点的主机名（内网短名）。
func bypassHost(override, host string) bool {
	override = strings.TrimSpace(override)
	if override == "" {
		return false
	}
	host = strings.ToLower(strings.TrimSpace(host))
	plain := strings.TrimSuffix(host, ".")
	for _, raw := range strings.Split(override, ";") {
		pat := strings.ToLower(strings.TrimSpace(raw))
		if pat == "" {
			continue
		}
		if pat == "<local>" {
			if !strings.Contains(plain, ".") && plain != "" {
				return true
			}
			continue
		}
		pat = strings.TrimPrefix(pat, "*")
		pat = strings.TrimPrefix(pat, ".")
		if pat == "" {
			continue
		}
		if plain == pat || strings.HasSuffix(plain, "."+pat) {
			return true
		}
	}
	return false
}

// proxyFuncFrom 由代理设置生成 http.Transport.Proxy。
// 未启用或地址为空时返回 nil（即直连）。
func proxyFuncFrom(set ProxySetting) func(*http.Request) (*url.URL, error) {
	if !set.Enabled || strings.TrimSpace(set.Server) == "" {
		return nil
	}
	return func(req *http.Request) (*url.URL, error) {
		if req == nil || req.URL == nil {
			return nil, nil
		}
		if bypassHost(set.Override, req.URL.Hostname()) {
			return nil, nil
		}
		return proxyURLFor(set, req.URL.Scheme), nil
	}
}

// normalizeProxyURL 补全手动代理地址的 scheme。支持 socks= 前缀写法。
func normalizeProxyURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	if strings.HasPrefix(strings.ToLower(raw), "socks=") {
		return "socks5://" + strings.TrimPrefix(raw, "socks=")
	}
	if strings.HasPrefix(strings.ToLower(raw), "socks5=") {
		return "socks5://" + strings.TrimPrefix(raw, "socks5=")
	}
	return "http://" + raw
}

// SetProxy 设置代理策略：system（默认，读系统/环境变量）、direct（直连）、
// manual（用 rawURL）。设置后新请求生效。
func (c *Client) SetProxy(mode, rawURL string) {
	if c == nil {
		return
	}
	c.init()
	if c.tr == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "direct", "off", "none":
		c.tr.Proxy = nil
	case "manual":
		u, err := url.Parse(normalizeProxyURL(rawURL))
		if err != nil || u.Host == "" {
			// 地址不合法时退回系统代理，避免把请求变成直连却不知道。
			c.tr.Proxy = systemProxy()
			return
		}
		c.tr.Proxy = http.ProxyURL(u)
	default:
		c.tr.Proxy = systemProxy()
	}
}

// hasProxyEnv 判断用户是否显式配了代理环境变量（显式配置优先于系统设置）。
func hasProxyEnv() bool {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if strings.TrimSpace(getEnv(k)) != "" {
			return true
		}
	}
	return false
}
