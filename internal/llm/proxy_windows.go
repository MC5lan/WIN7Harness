//go:build windows

package llm

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/win32"
)

const inetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// ReadSystemProxy 读取 WinINET（IE/系统设置）里的静态代理配置。
// 只读注册表，不修改任何系统设置。PAC/WPAD 由 systemProxy 按请求解析。
func ReadSystemProxy() ProxySetting {
	for _, root := range []uintptr{win32.HKEY_CURRENT_USER, win32.HKEY_LOCAL_MACHINE} {
		on, ok := win32.RegGetDWORD(root, inetSettingsKey, "ProxyEnable")
		if !ok || on == 0 {
			continue
		}
		server, _ := win32.RegGetString(root, inetSettingsKey, "ProxyServer")
		if server == "" {
			core.Logf("llm: 系统代理已启用（ProxyEnable=1，%s）但 ProxyServer 为空或不可读，按直连处理", hkRootName(root))
			continue
		}
		override, _ := win32.RegGetString(root, inetSettingsKey, "ProxyOverride")
		return ProxySetting{Enabled: true, Server: server, Override: override}
	}
	// PAC/WPAD 不在这里返回静态地址；systemProxy 会按请求解析。
	pac, _ := win32.RegGetString(win32.HKEY_CURRENT_USER, inetSettingsKey, "AutoConfigURL")
	auto, _ := win32.RegGetDWORD(win32.HKEY_CURRENT_USER, inetSettingsKey, "AutoDetect")
	if strings.TrimSpace(pac) != "" || auto == 1 {
		pacHintOnce.Do(func() {
			if strings.TrimSpace(pac) != "" {
				core.Logf("llm: 检测到 PAC（%s），将按请求解析代理", pac)
			} else {
				core.Logf("llm: 检测到 WPAD 自动检测，将按请求解析代理")
			}
		})
	}
	return ProxySetting{}
}

var pacHintOnce sync.Once

func hkRootName(root uintptr) string {
	if root == win32.HKEY_LOCAL_MACHINE {
		return "HKLM"
	}
	return "HKCU"
}

// systemProxy 给出 Transport 用的代理选择函数：
// 环境变量显式配置优先，其次 WinINET 静态代理，最后按 URL 解析 PAC/WPAD。
func systemProxy() func(*http.Request) (*url.URL, error) {
	if hasProxyEnv() {
		return http.ProxyFromEnvironment
	}
	if set := ReadSystemProxy(); set.Enabled {
		return proxyFuncFrom(set)
	}
	return pacProxyFunc()
}

type pacEntry struct {
	proxy  string
	bypass string
	direct bool
	at     time.Time
}

var (
	pacMu    sync.Mutex
	pacCache = map[string]pacEntry{}
)

// pacProxyFunc 按目标 host 缓存 PAC 解析结果。PAC 可能按域名返回不同代理，
// 所以不能用单一全局代理；这里以 scheme://host 为键，2 分钟过期。
func pacProxyFunc() func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		if req == nil || req.URL == nil {
			return nil, nil
		}
		key := strings.ToLower(req.URL.Scheme + "://" + req.URL.Host)
		now := time.Now()
		pacMu.Lock()
		e, ok := pacCache[key]
		pacMu.Unlock()
		if !ok || now.Sub(e.at) > 2*time.Minute {
			proxy, bypass, direct, parsed := win32.ResolvePACProxy(req.URL.String())
			e = pacEntry{proxy: proxy, bypass: bypass, direct: direct, at: now}
			if !parsed {
				// 没有 PAC/WPAD，或 WinHTTP 解析失败：直连。
				e.direct = true
			}
			pacMu.Lock()
			pacCache[key] = e
			pacMu.Unlock()
		}
		if e.direct || strings.TrimSpace(e.proxy) == "" {
			return nil, nil
		}
		if bypassHost(e.bypass, req.URL.Hostname()) {
			return nil, nil
		}
		set := ProxySetting{Enabled: true, Server: e.proxy, Override: e.bypass}
		return proxyURLFor(set, req.URL.Scheme), nil
	}
}

// ChildProxyEnv 给子进程（git/go/npm/pip…）生成代理环境变量。
// 静态代理直接使用；PAC 只解析一个代表目标，因为环境变量无法按域名分流。
// 如果企业代理要求 NTLM/Kerberos 认证，子进程仍可能 407；见文档说明。
// SystemProxyConfigured 判断当前是否存在静态代理、环境变量代理或 PAC/WPAD。
// 只有存在时才值得启动本机 NTLM 转发桥；否则直连更简单。
func SystemProxyConfigured() bool {
	mode, raw := proxyMode()
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "direct", "off", "none":
		return false
	case "manual":
		return strings.TrimSpace(raw) != ""
	}
	if hasProxyEnv() {
		return true
	}
	if ReadSystemProxy().Enabled {
		return true
	}
	for _, root := range []uintptr{win32.HKEY_CURRENT_USER, win32.HKEY_LOCAL_MACHINE} {
		if pac, _ := win32.RegGetString(root, inetSettingsKey, "AutoConfigURL"); strings.TrimSpace(pac) != "" {
			return true
		}
		if auto, _ := win32.RegGetDWORD(root, inetSettingsKey, "AutoDetect"); auto == 1 {
			return true
		}
	}
	return false
}

func proxyMode() (mode, raw string) {
	if cfg := core.Get(); cfg != nil {
		return cfg.ProxyMode, cfg.ProxyURL
	}
	return "", ""
}

// ResolveProxyForURL 给本机转发桥用：按目标 URL 返回上游代理，nil 表示直连。
// 与 systemProxy 的区别是不走 http.Transport，也不做按 host 缓存。
type resolvedProxy struct {
	u   *url.URL
	err error
	at  time.Time
}

var (
	resolveMu    sync.Mutex
	resolveCache = map[string]resolvedProxy{}
)

// ResetProxyCache 在用户改代理/睡眠恢复后清掉按 host 的 PAC 缓存。
func ResetProxyCache() {
	resolveMu.Lock()
	resolveCache = map[string]resolvedProxy{}
	resolveMu.Unlock()
}

func ResolveProxyForURL(u *url.URL) (*url.URL, error) {
	if u == nil || u.Host == "" {
		return nil, fmt.Errorf("目标 URL 不合法")
	}
	if isLoopbackHost(u.Hostname()) {
		return nil, nil
	}
	key := strings.ToLower(u.Scheme + "://" + u.Host)
	now := time.Now()
	resolveMu.Lock()
	e, ok := resolveCache[key]
	resolveMu.Unlock()
	if ok && now.Sub(e.at) < 2*time.Minute {
		if e.u == nil {
			return nil, e.err
		}
		cp := *e.u
		return &cp, e.err
	}
	pu, err := resolveProxyForURLUncached(u)
	resolveMu.Lock()
	resolveCache[key] = resolvedProxy{u: pu, err: err, at: now}
	resolveMu.Unlock()
	if pu == nil {
		return nil, err
	}
	cp := *pu
	return &cp, err
}

func resolveProxyForURLUncached(u *url.URL) (*url.URL, error) {
	target := u.String()
	mode, raw := proxyMode()
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "direct", "off", "none":
		return nil, nil
	case "manual":
		pu, err := url.Parse(normalizeProxyURL(raw))
		if err != nil || pu.Host == "" {
			return nil, fmt.Errorf("手动代理地址不合法：%q", raw)
		}
		return pu, nil
	}
	if hasProxyEnv() {
		return http.ProxyFromEnvironment(&http.Request{URL: u})
	}
	if set := ReadSystemProxy(); set.Enabled {
		return proxyURLFor(set, u.Scheme), nil
	}
	proxy, bypass, direct, ok := win32.ResolvePACProxy(target)
	if !ok || direct || strings.TrimSpace(proxy) == "" {
		return nil, nil
	}
	set := ProxySetting{Enabled: true, Server: proxy, Override: bypass}
	return proxyURLFor(set, u.Scheme), nil
}

func ChildProxyEnv(target string) map[string]string {
	mode, raw := proxyMode()
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "direct", "off", "none":
		return nil
	case "manual":
		u, err := url.Parse(normalizeProxyURL(raw))
		if err == nil && u.Host != "" {
			return proxyEnvMap(u.String(), "")
		}
	}
	if hasProxyEnv() {
		return nil
	}
	if strings.TrimSpace(target) == "" {
		target = "https://github.com"
	}
	if set := ReadSystemProxy(); set.Enabled {
		addr, socks := parseProxyTarget(set.Server, "https")
		if socks {
			addr = "socks5://" + addr
		}
		return proxyEnvMap(addr, set.Override)
	}
	proxy, bypass, direct, ok := win32.ResolvePACProxy(target)
	if !ok || direct || strings.TrimSpace(proxy) == "" {
		return nil
	}
	addr, socks := parseProxyTarget(proxy, "https")
	if socks {
		addr = "socks5://" + addr
	}
	return proxyEnvMap(addr, bypass)
}

func proxyEnvMap(addr, override string) map[string]string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	env := map[string]string{
		"HTTP_PROXY":  addr,
		"HTTPS_PROXY": addr,
	}
	no := noProxyList(override)
	if no != "" {
		env["NO_PROXY"] = no
	}
	return env
}

// noProxyList 把 WinINET 的 ProxyOverride（分号分隔、支持 <local>）转成
// Go/Git/npm 都能识别的 NO_PROXY（逗号分隔）。
func noProxyList(override string) string {
	out := []string{"localhost", "127.0.0.1", "::1"}
	for _, raw := range strings.Split(override, ";") {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		if strings.EqualFold(p, "<local>") {
			out = append(out, ".local")
			continue
		}
		p = strings.TrimPrefix(p, "*")
		p = strings.TrimPrefix(p, ".")
		if p != "" {
			out = append(out, p)
		}
	}
	// 去重
	seen := map[string]bool{}
	uniq := out[:0]
	for _, s := range out {
		if !seen[s] {
			seen[s] = true
			uniq = append(uniq, s)
		}
	}
	return strings.Join(uniq, ",")
}
