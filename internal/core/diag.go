package core

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DiagOptions 控制诊断包内容。默认全开、包含最近会话摘要，但不含对话正文。
type DiagOptions struct {
	IncludeChecks bool
	IncludeConfig bool
	IncludeLogs   bool
	IncludeEnv    bool
	SessionFiles  int // 列出最近 N 个会话文件的名称与大小（不含内容）

	// Extra 附加文件：文件名 -> 内容（v1.29 起用来塞运行指标 metrics.txt）。
	// app 层才知道指标实例在哪，core 只负责写进 zip。
	Extra map[string]string
}

// DefaultDiagOptions 面向用户点「导出诊断包」时的默认组合。
func DefaultDiagOptions() DiagOptions {
	return DiagOptions{IncludeChecks: true, IncludeConfig: true, IncludeLogs: true, IncludeEnv: true, SessionFiles: 20}
}

// ExportDiag 生成一个 zip 诊断包，返回其绝对路径。
// 内容全部为本机只读采集，不含 API Key 明文与对话正文。
func ExportDiag(outPath string, opt DiagOptions) (string, error) {
	if outPath == "" {
		dir := filepath.Join(LocalDataDir(), "exports")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		outPath = filepath.Join(dir, "harness-diag-"+time.Now().Format("20060102-150405")+".zip")
	} else if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}

	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)
	write := func(name, body string) {
		w, e := zw.Create(name)
		if e != nil {
			return
		}
		_, _ = io.WriteString(w, body)
	}

	write("README.txt", diagReadme())

	if opt.IncludeEnv {
		write("env.txt", envDump())
	}
	if opt.IncludeChecks {
		write("checks.txt", Report(RunChecks(nil)))
	}
	if opt.IncludeConfig {
		write("config.redacted.json", RedactedConfigJSON())
	}
	if opt.IncludeLogs {
		write("logs.txt", strings.Join(LogTail(2000), "\r\n"))
	}
	if opt.SessionFiles > 0 {
		write("sessions.txt", sessionListing(opt.SessionFiles))
	}
	for name, body := range opt.Extra {
		if name == "" {
			continue
		}
		write(name, body)
	}

	if err := zw.Close(); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	Logf("已导出诊断包：%s", outPath)
	return outPath, nil
}

func diagReadme() string {
	return "WIN7 Harness 诊断包\r\n" +
		"======================\r\n\r\n" +
		"用途：排查“为什么在我这台机器上跑不起来”，请把整个 zip 发给支持方。\r\n\r\n" +
		"包含：\r\n" +
		"  env.txt               运行环境（系统版本、位数、路径）\r\n" +
		"  checks.txt            自检结果与修复建议\r\n" +
		"  config.redacted.json  配置（API Key 已脱敏，仅保留首尾各 4 位）\r\n" +
		"  logs.txt              最近日志\r\n" +
		"  sessions.txt          最近会话文件名与大小（不含对话内容）\r\n\r\n" +
		"不包含：API Key 明文、对话正文、工作目录里的任何文件。\r\n"
}

func envDump() string {
	var b strings.Builder
	fmt.Fprintf(&b, "程序版本: %s\r\n", Version)
	fmt.Fprintf(&b, "系统: %s\r\n", osVersion())
	fmt.Fprintf(&b, "进程位数: %d\r\n", strconvIntSize())
	fmt.Fprintf(&b, "数据目录: %s\r\n", LocalDataDir())
	if c := Get(); c != nil {
		fmt.Fprintf(&b, "工作目录: %s\r\n", c.WorkDir)
		fmt.Fprintf(&b, "活动端点: %s\r\n", c.Provider().BaseURL)
		fmt.Fprintf(&b, "模型: %s\r\n", c.Model)
	}
	if free, total, err := diskFree(LocalDataDir()); err == nil {
		fmt.Fprintf(&b, "数据盘: 剩余 %.1f GB / 共 %.1f GB\r\n",
			float64(free)/1073741824, float64(total)/1073741824)
	}
	fmt.Fprintf(&b, "生成时间: %s\r\n", time.Now().Format("2006-01-02 15:04:05"))
	return b.String()
}

// RedactedConfigJSON 返回脱敏后的配置文本，可直接贴给他人。
func RedactedConfigJSON() string {
	c := Get()
	if c == nil {
		return "{}"
	}
	cp := *c
	cp.Providers = make([]Provider, len(c.Providers))
	copy(cp.Providers, c.Providers)
	// 便携密钥密文也不进支持包；虽然解不开，但没必要暴露。
	cp.PortableSecrets = ""
	for i := range cp.Providers {
		cp.Providers[i].APIKey = maskSecret(cp.Providers[i].APIKey)
		// 密文也不该出现在支持包里：虽然解不开，但没必要暴露。
		cp.Providers[i].APIKeyEnc = ""
		cp.Providers[i].BaseURL = maskURLUserinfo(cp.Providers[i].BaseURL)
	}
	// MCP：命令行、环境变量、请求头、参数都可能带密钥——
	// 只打 APIKey 会把远程 MCP 的 Bearer 原样漏出去。
	cp.MCPServers = make([]MCPServer, len(c.MCPServers))
	copy(cp.MCPServers, c.MCPServers)
	for i := range cp.MCPServers {
		m := &cp.MCPServers[i]
		m.Headers = m.Redacted()
		m.Args = redactArgs(m.Args)
		m.Env = redactArgs(m.Env)
		m.URL = maskURLUserinfo(m.URL)
	}
	// Hooks：命令行里可能夹带 token/密钥，保守整串打码。
	cp.Hooks = make([]Hook, len(c.Hooks))
	copy(cp.Hooks, c.Hooks)
	for i := range cp.Hooks {
		if looksSecretValue(cp.Hooks[i].Command) {
			cp.Hooks[i].Command = "***（含疑似密钥，已打码）***"
		}
	}
	// 代理可能是 http://user:pass@host:port
	if cp.ProxyURL != "" {
		cp.ProxyURL = maskURLUserinfo(cp.ProxyURL)
	}
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

// redactArgs 对参数/环境变量项打码：--flag=value 保留旗标只遮值，
// 裸项整段打码，KEY=VALUE 保留键名只遮值。
func redactArgs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, a := range in {
		la := strings.ToLower(a)
		secretish := strings.Contains(la, "token") || strings.Contains(la, "key") ||
			strings.Contains(la, "secret") || strings.Contains(la, "password") ||
			strings.Contains(la, "authorization") || strings.Contains(la, "cookie")
		if i := strings.Index(a, "="); i >= 0 {
			out = append(out, a[:i+1]+"***")
			continue
		}
		if secretish {
			out = append(out, "***")
			continue
		}
		out = append(out, a)
	}
	return out
}

// maskURLUserinfo 把 URL 里的 userinfo 打码：http://user:pass@host → http://***@host
func maskURLUserinfo(u string) string {
	schemeEnd := strings.Index(u, "://")
	if schemeEnd < 0 {
		return u
	}
	rest := u[schemeEnd+3:]
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return u
	}
	return u[:schemeEnd+3] + "***@" + rest[at+1:]
}

// looksSecretValue 判断命令行/字符串里是否疑似夹带密钥。
func looksSecretValue(s string) bool {
	ls := strings.ToLower(s)
	for _, w := range []string{"token", "key", "secret", "password", "authorization", "cookie"} {
		if strings.Contains(ls, w) {
			return true
		}
	}
	return false
}

func sessionListing(limit int) string {
	dir := SessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "（无会话目录）"
	}
	type item struct {
		name string
		size int64
		at   time.Time
	}
	var items []item
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{e.Name(), info.Size(), info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].at.After(items[j].at) })
	var b strings.Builder
	fmt.Fprintf(&b, "共 %d 个会话文件，列出最近 %d 个（不含正文）\r\n", len(items), limit)
	for i, it := range items {
		if i >= limit {
			break
		}
		fmt.Fprintf(&b, "%-40s %8.1f KB  %s\r\n", it.name, float64(it.size)/1024, it.at.Format("2006-01-02 15:04"))
	}
	return b.String()
}

// maskSecret 保留首尾各 4 位，其余打码；空值原样返回。
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
