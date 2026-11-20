//go:build windows

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"win7harness/internal/core"
	"win7harness/internal/llm"
	"win7harness/internal/netbridge"
)

var (
	caOnce   sync.Once
	caPath   string
	caEnough bool
)

// childNetEnv 让 git/go/npm/pip/curl 复用 Windows 的系统代理和系统根证书。
//
// 背景：应用自己的 HTTP 客户端有 PAC/内置 CA，子进程却只看环境变量：
// 企业内网常出现“聊天能连、go mod download 连不上”或
// “浏览器能开、git 报 unknown authority”。这里把两者补齐。
//
// NTLM/Kerberos 代理认证不在环境变量能力范围内；需要网管提供 Basic 代理
// 或 VPN/本机网关。纯 PAC 解析已支持。
func childNetEnv() []string {
	caOnce.Do(func() {
		caPath = filepath.Join(core.LocalDataDir(), "ca-bundle.pem")
		extra := ""
		if cfg := core.Get(); cfg != nil {
			extra = cfg.TLSExtraCA
		}
		if err := llm.ExportCABundle(caPath, extra); err != nil {
			core.Logf("netenv: 导出合并 CA 失败：%v（子进程可能仍不信任企业根证书）", err)
			return
		}
		caEnough = true
		core.Logf("netenv: 已导出系统 CA 合并包：%s", caPath)
	})

	var out []string
	target := "https://github.com"
	if cfg := core.Get(); cfg != nil {
		if base := cfg.Provider().BaseURL; base != "" {
			target = base
		}
	}
	// 代理每次重新取：设置页改代理后，下一条命令立即生效。
	proxyEnv := map[string]string{}
	bridgeActive := false
	if llm.SystemProxyConfigured() {
		if s, err := netbridge.Ensure(llm.ResolveProxyForURL); err == nil && s != nil {
			// 子进程只认本机桥；桥内部再按目标解析 PAC/静态代理并完成 NTLM。
			// 这里必须覆盖父进程里可能存在的上游 HTTP_PROXY：否则 NTLM 环境下
			// 子进程绕过桥，仍然会 407。
			bridgeActive = true
			proxyEnv["HTTP_PROXY"] = s.URL
			proxyEnv["HTTPS_PROXY"] = s.URL
			proxyEnv["ALL_PROXY"] = s.URL
			no := "localhost,127.0.0.1,::1"
			if parent := strings.TrimSpace(os.Getenv("NO_PROXY")); parent != "" {
				no = parent + "," + no
			}
			proxyEnv["NO_PROXY"] = no
		} else if err != nil {
			core.Logf("netenv: 本机代理桥不可用，回退环境变量代理：%v", err)
		}
	}
	if len(proxyEnv) == 0 {
		proxyEnv = llm.ChildProxyEnv(target)
	}
	for k, v := range proxyEnv {
		if (bridgeActive || os.Getenv(k) == "") && v != "" {
			out = append(out, k+"="+v)
		}
	}
	if caEnough {
		for _, k := range []string{
			"SSL_CERT_FILE",
			"GIT_SSL_CAINFO",
			"NODE_EXTRA_CA_CERTS",
			"REQUESTS_CA_BUNDLE",
			"CURL_CA_BUNDLE",
			"PIP_CERT",
			"AWS_CA_BUNDLE",
		} {
			if os.Getenv(k) == "" {
				out = append(out, k+"="+caPath)
			}
		}
	}
	return out
}
