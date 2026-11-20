package core

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// 检查状态。
const (
	CheckOK   = "ok"
	CheckWarn = "warn"
	CheckFail = "fail"
	CheckSkip = "skip"
)

// CheckResult 一条自检结果。
type CheckResult struct {
	Name   string // 检查项名称
	Status string // ok / warn / fail / skip
	Detail string // 观测到的实际情况
	Hint   string // 不通过时的修复建议
}

// RunChecks 执行全套环境自检。全部为只读探测，不修改任何系统设置。
func RunChecks(cfg *Config) []CheckResult {
	if cfg == nil {
		cfg = Get()
	}
	var out []CheckResult
	add := func(r CheckResult) { out = append(out, r) }

	// 1. 配置目录/本机数据目录可写
	if samePath(DataDir(), LocalDataDir()) {
		add(checkDirWritable("数据目录", DataDir()))
	} else {
		add(checkDirWritable("配置目录", DataDir()))
		add(checkDirWritable("数据目录", LocalDataDir()))
	}
	// 2. 工作目录可用
	add(checkWorkDir(cfg.WorkDir))
	// 3. 磁盘空间（会话/日志在本机目录，优先检查它）
	add(checkDiskFree(LocalDataDir()))
	// 4. 模型配置
	add(checkProvider(cfg))
	// 5. TLS 握手
	add(checkTLS(cfg))
	// 6. 外部工具
	for _, name := range []string{"git", "node", "npx", "python"} {
		add(checkTool(name))
	}
	// 7. 平台信息
	out = append(out, platformChecks()...)
	return out
}

func checkDirWritable(name, dir string) CheckResult {
	r := CheckResult{Name: name + "可写"}
	if strings.TrimSpace(dir) == "" {
		r.Status, r.Detail, r.Hint = CheckFail, "路径为空", "在设置页重新选择工作目录"
		return r
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.Status, r.Detail = CheckFail, "无法创建："+err.Error()
		r.Hint = "换一个有写权限的目录，或检查杀毒软件拦截"
		return r
	}
	f := filepath.Join(dir, ".harness_write_test")
	if err := WriteFileAtomic(f, []byte("ok"), 0o644); err != nil {
		r.Status, r.Detail = CheckFail, "写入失败："+err.Error()
		r.Hint = "目录只读或被安全软件保护，请更换目录或加白名单"
		return r
	}
	_ = os.Remove(f)
	r.Status, r.Detail = CheckOK, dir
	return r
}

func checkWorkDir(dir string) CheckResult {
	r := CheckResult{Name: "工作目录"}
	if strings.TrimSpace(dir) == "" {
		r.Status, r.Detail, r.Hint = CheckFail, "未设置", "设置页选择项目目录"
		return r
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		r.Status, r.Detail, r.Hint = CheckFail, "不存在或不是目录："+dir, "设置页重新选择目录"
		return r
	}
	tmp := filepath.Join(dir, ".harness_probe")
	if err := WriteFileAtomic(tmp, []byte("ok"), 0o644); err != nil {
		r.Status, r.Detail = CheckWarn, "可读但不可写："+dir
		r.Hint = "只能做只读调研；如需改代码请换有写权限的目录"
		return r
	}
	_ = os.Remove(tmp)
	r.Status, r.Detail = CheckOK, dir
	return r
}

func checkDiskFree(dir string) CheckResult {
	r := CheckResult{Name: "磁盘剩余空间"}
	free, total, err := diskFree(dir)
	if err != nil {
		r.Status, r.Detail = CheckSkip, "无法读取："+err.Error()
		return r
	}
	const mb = 1024 * 1024
	r.Detail = fmt.Sprintf("剩余 %.1f GB / 共 %.1f GB", float64(free)/mb/1024, float64(total)/mb/1024)
	switch {
	case free < 100*mb:
		r.Status = CheckFail
		r.Hint = "磁盘几乎满了，日志、快照、会话都会写入失败，请先清理"
	case free < 500*mb:
		r.Status = CheckWarn
		r.Hint = "空间偏紧，长任务可能因快照失败中断"
	default:
		r.Status = CheckOK
	}
	return r
}

func checkProvider(cfg *Config) CheckResult {
	r := CheckResult{Name: "模型配置"}
	p := cfg.Provider()
	var miss []string
	if strings.TrimSpace(p.BaseURL) == "" {
		miss = append(miss, "接口地址")
	}
	if strings.TrimSpace(p.APIKey) == "" && !isLocalEndpoint(p.BaseURL) {
		miss = append(miss, "API Key")
	}
	model := cfg.Model
	if model == "" {
		model = p.Model
	}
	if strings.TrimSpace(model) == "" {
		miss = append(miss, "模型名")
	}
	if len(miss) > 0 {
		r.Status, r.Detail = CheckFail, "缺少："+strings.Join(miss, "、")
		r.Hint = "设置页补齐后点「测试连接」"
		return r
	}
	r.Status, r.Detail = CheckOK, fmt.Sprintf("%s / %s", hostOf(p.BaseURL), model)
	return r
}

func checkTLS(cfg *Config) CheckResult {
	r := CheckResult{Name: "TLS 握手"}
	base := cfg.Provider().BaseURL
	host := hostOf(base)
	if host == "" {
		r.Status, r.Detail = CheckSkip, "未配置接口地址"
		return r
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", host, &tls.Config{ServerName: splitHost(host)})
	if err != nil {
		r.Status, r.Detail = CheckFail, err.Error()
		low := strings.ToLower(err.Error())
		switch {
		case strings.Contains(low, "certificate"), strings.Contains(low, "x509"):
			r.Hint = "证书链校验失败：Win7 需安装 KB4474419（SHA-2 支持）与 KB4490628"
		case strings.Contains(low, "timeout"), strings.Contains(low, "refused"):
			r.Hint = "网络不通：检查代理、防火墙，或换用可直连的端点"
		default:
			r.Hint = "Win7 常见原因：缺少 SHA-2 补丁、TLS 1.2 未启用、系统根证书过期"
		}
		return r
	}
	ver := tlsVersionName(conn.ConnectionState().Version)
	_ = conn.Close()
	r.Status, r.Detail = CheckOK, host+" · "+ver
	return r
}

func checkTool(name string) CheckResult {
	r := CheckResult{Name: "外部命令 " + name}
	path, err := exec.LookPath(name)
	if err != nil {
		r.Status, r.Detail = CheckSkip, "未安装"
		switch name {
		case "git":
			r.Status = CheckWarn
			r.Hint = "未安装 git：/diff、/commit 不可用，安装后重启程序即可"
		case "npx", "node":
			r.Detail = "未安装 Node.js"
			r.Hint = "只有使用 MCP 服务时才需要；安装 Node 后 /mcp reload"
		case "python":
			r.Hint = "需要执行 Python 脚本的任务会失败，可安装后重试"
		}
		return r
	}
	r.Status, r.Detail = CheckOK, path
	return r
}

func hostOf(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "http" {
			host += ":80"
		} else {
			host += ":443"
		}
	}
	return host
}

func splitHost(hostport string) string {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return hostport
	}
	return h
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	}
	return fmt.Sprintf("TLS 0x%04x", v)
}

func isLocalEndpoint(baseURL string) bool {
	low := strings.ToLower(baseURL)
	return strings.Contains(low, "localhost") || strings.Contains(low, "127.0.0.1") ||
		strings.Contains(low, "192.168.") || strings.Contains(low, "10.") || strings.Contains(low, "ollama")
}

// Report 把自检结果渲染成可复制的文本报告。
func Report(results []CheckResult) string {
	var b strings.Builder
	fails, warns := 0, 0
	for _, r := range results {
		if r.Status == CheckFail {
			fails++
		} else if r.Status == CheckWarn {
			warns++
		}
	}
	mark := func(s string) string {
		switch s {
		case CheckOK:
			return "[通过]"
		case CheckWarn:
			return "[注意]"
		case CheckFail:
			return "[失败]"
		}
		return "[跳过]"
	}
	fmt.Fprintf(&b, "自检结果：%d 项，失败 %d，注意 %d\n", len(results), fails, warns)
	fmt.Fprintf(&b, "系统：%s %s｜程序 v%s｜进程 %d 位\n", osVersion(), runtime.GOOS, Version, strconvIntSize())
	for _, r := range results {
		fmt.Fprintf(&b, "%s %s：%s\n", mark(r.Status), r.Name, r.Detail)
		if r.Hint != "" && r.Status != CheckOK {
			fmt.Fprintf(&b, "      建议：%s\n", r.Hint)
		}
	}
	return b.String()
}

func strconvIntSize() int { return 32 << (^uint(0) >> 63) }

// isProcess32 判断当前进程是否为 32 位，用于提示 2GB 地址空间上限。
func isProcess32() bool { return strconvIntSize() == 32 }

func osArchName() string { return runtime.GOARCH }
