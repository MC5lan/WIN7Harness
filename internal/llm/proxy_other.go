//go:build !windows

package llm

import (
	"net/http"
	"net/url"
)

// ReadSystemProxy 非 Windows 平台没有 WinINET，返回空设置。
func ReadSystemProxy() ProxySetting { return ProxySetting{} }

// systemProxy 非 Windows 平台只看环境变量。
func systemProxy() func(*http.Request) (*url.URL, error) {
	return http.ProxyFromEnvironment
}

// SystemProxyConfigured 非 Windows 只看环境变量代理。
func SystemProxyConfigured() bool { return hasProxyEnv() }

// ResetProxyCache 非 Windows 没有 PAC 缓存。
func ResetProxyCache() {}

// ResolveProxyForURL 非 Windows 不做 PAC；交给 Go 的环境变量代理。
func ResolveProxyForURL(u *url.URL) (*url.URL, error) {
	if u == nil || isLoopbackHost(u.Hostname()) {
		return nil, nil
	}
	return http.ProxyFromEnvironment(&http.Request{URL: u})
}
