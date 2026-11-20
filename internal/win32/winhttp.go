//go:build windows

package win32

import (
	"strings"
	"syscall"
	"unsafe"
)

// WinHTTP 只用来解析 PAC/WPAD：Win7 的 IE“自动配置脚本/自动检测设置”在
// 企业内网很常见，而 WinINET 注册表里往往没有静态 ProxyServer。
// 这里不接管请求，只回答“这个 URL 应该走哪个代理”，后面的 HTTP/TLS
// 仍由 Go 的 Transport 负责（证书策略可继续用内置根证书兜底）。

var (
	dWinhttp = syscall.NewLazyDLL("winhttp.dll")

	pWinHttpOpen           = dWinhttp.NewProc("WinHttpOpen")
	pWinHttpGetProxyForUrl = dWinhttp.NewProc("WinHttpGetProxyForUrl")
	pWinHttpCloseHandle    = dWinhttp.NewProc("WinHttpCloseHandle")
)

const (
	winHTTPAccessTypeNoProxy = 1

	winHTTPAutoProxyAutoDetect = 0x00000001
	winHTTPAutoProxyConfigURL  = 0x00000002
	winHTTPAutoDetectDHCP      = 0x00000001
	winHTTPAutoDetectDNSA      = 0x00000002
)

type winHTTPAutoProxyOptions struct {
	Flags                 uint32
	AutoDetectFlags       uint32
	AutoConfigURL         *uint16
	Reserved              uintptr
	Reserved2             uint32
	AutoLogonIfChallenged int32
}

type winHTTPProxyInfo struct {
	AccessType  uint32
	Proxy       *uint16
	ProxyBypass *uint16
}

// ResolvePACProxy 用 WinHTTP 解析 PAC/WPAD，返回该 URL 的代理与绕过列表。
// direct=true 表示 PAC 明确要求直连；ok=false 表示系统没有 PAC/WPAD 或解析失败。
func ResolvePACProxy(targetURL string) (proxy, bypass string, direct, ok bool) {
	pac, _ := RegGetString(HKEY_CURRENT_USER, inetSettingsKeyForWinHTTP, "AutoConfigURL")
	auto, _ := RegGetDWORD(HKEY_CURRENT_USER, inetSettingsKeyForWinHTTP, "AutoDetect")
	if strings.TrimSpace(pac) == "" && auto != 1 {
		// 部分企业通过 HKLM 下发 IE 自动配置。
		pac, _ = RegGetString(HKEY_LOCAL_MACHINE, inetSettingsKeyForWinHTTP, "AutoConfigURL")
		auto, _ = RegGetDWORD(HKEY_LOCAL_MACHINE, inetSettingsKeyForWinHTTP, "AutoDetect")
	}
	if strings.TrimSpace(pac) == "" && auto != 1 {
		return "", "", true, false
	}
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return "", "", true, false
	}
	if err := pWinHttpOpen.Find(); err != nil {
		return "", "", true, false
	}
	session, _, _ := pcall(pWinHttpOpen, 0, winHTTPAccessTypeNoProxy, 0, 0, 0)
	if session == 0 {
		return "", "", true, false
	}
	defer pcall(pWinHttpCloseHandle, session)

	opts := winHTTPAutoProxyOptions{AutoLogonIfChallenged: 1}
	if strings.TrimSpace(pac) != "" {
		u, err := syscall.UTF16PtrFromString(strings.TrimSpace(pac))
		if err != nil {
			return "", "", true, false
		}
		opts.Flags = winHTTPAutoProxyConfigURL
		opts.AutoConfigURL = u
	} else {
		opts.Flags = winHTTPAutoProxyAutoDetect
		opts.AutoDetectFlags = winHTTPAutoDetectDHCP | winHTTPAutoDetectDNSA
	}
	urlPtr, err := syscall.UTF16PtrFromString(targetURL)
	if err != nil {
		return "", "", true, false
	}
	var info winHTTPProxyInfo
	r, _, _ := pcall(pWinHttpGetProxyForUrl,
		session,
		uintptr(unsafe.Pointer(urlPtr)),
		uintptr(unsafe.Pointer(&opts)),
		uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return "", "", true, false
	}
	if info.Proxy != nil {
		defer pGlobalFree.Call(uintptr(unsafe.Pointer(info.Proxy)))
	}
	if info.ProxyBypass != nil {
		defer pGlobalFree.Call(uintptr(unsafe.Pointer(info.ProxyBypass)))
	}
	proxy = utf16PtrString(info.Proxy)
	bypass = utf16PtrString(info.ProxyBypass)
	// WinHTTP 的 WINHTTP_PROXY_INFO.dwAccessType：1 = 直连，2 = 命名代理。
	// 某些 Win7/WinHTTP 组合在 PAC 命中时返回 3 但带 Proxy 字符串；以
	// “有没有代理地址”为准，只有明确 1 或空地址才当直连。
	if info.AccessType == 1 || strings.TrimSpace(proxy) == "" {
		return "", bypass, true, true
	}
	return proxy, bypass, false, true
}

// inetSettingsKeyForWinHTTP 与 llm 读取的键一致；放在 win32 里避免循环依赖。
const inetSettingsKeyForWinHTTP = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func utf16PtrString(p *uint16) string {
	if p == nil {
		return ""
	}
	n := 0
	for {
		if *(*uint16)(unsafe.Pointer(uintptr(unsafe.Pointer(p)) + uintptr(n)*2)) == 0 {
			break
		}
		n++
		if n > 1<<20 {
			return ""
		}
	}
	return syscall.UTF16ToString(unsafe.Slice(p, n))
}
