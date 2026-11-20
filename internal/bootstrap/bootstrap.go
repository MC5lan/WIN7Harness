// Package bootstrap 把「配置 → 网络客户端 → 引擎」的装配逻辑收在一处。
// 图形界面与命令行（-run）都走这里，避免两处各写一份 TLS / 代理 / 超时的处理，
// 也避免以后改网络行为时漏改一边。
package bootstrap

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"win7harness/internal/agent"
	"win7harness/internal/core"
	"win7harness/internal/llm"
	"win7harness/internal/netbridge"
)

// Options 装配引擎时的可选注入项。
type Options struct {
	// Emit 事件回调；命令行模式用来往 stdout 打流式输出。
	Emit func(agent.Event)
	// Confirm 危险操作确认；为空表示一律拒绝（命令行下的安全默认）。
	Confirm func(title, detail string) bool
	// ConfirmDiff 富差分确认（行号/配色/逐 hunk）；为空时退化为 Confirm 的纯文本弹窗。
	ConfirmDiff func(title, path string, hunks []core.DiffHunk, allowPartial bool) []int
	// PlanApprove 方案审批闸门；为空时保持旧的"填输入框"行为。
	PlanApprove func(title, plan string, steps int) (decision, modified string)
	// PlanMode 只读模式。
	PlanMode bool
}

// NewClientFor 用一个指定端点构造客户端，供会话级模型覆盖使用。
// 与 NewClient 的区别只有端点来源：这里是会话选中的服务商，不是全局活动服务商。
func NewClientFor(cfg *core.Config, p core.Provider, model string) (client *llm.Client, used string, warn string) {
	used = strings.TrimSpace(model)
	if used == "" {
		used = p.Model
	}
	used = core.NormalizeModelID(p.BaseURL, used)
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	tlsOpt := llm.TLSOptions{ExtraCAFile: cfg.TLSExtraCA, Insecure: cfg.TLSInsecure}
	c, err := llm.NewWithTLS(p.BaseURL, p.APIKey, used, timeout, tlsOpt)
	if err != nil {
		warn = "TLS 设置生效失败，已使用系统默认证书校验：" + err.Error()
		core.Logf("bootstrap: %s", warn)
		c = llm.New(p.BaseURL, p.APIKey, used, timeout)
	}
	if cfg.TLSInsecure {
		// 跳过证书校验 == API Key 明文可篡改上送。这是用户主动勾的，但也要说出来。
		warn = appendWarn(warn, "已跳过 TLS 证书校验：API Key 可能被链路上的中间人窃取（TLSInsecure=true）")
		core.Logf("bootstrap: TLSInsecure=true —— 到 %s 的流量不校验证书", p.BaseURL)
	}
	applyProxy(c, cfg)
	return c, used, warn
}

// NewClient 按当前配置构造模型客户端。
// 返回的 warn 非空表示 TLS 配置没能生效、已退回默认行为（不阻断使用）。
func NewClient(cfg *core.Config) (client *llm.Client, model, warn string) {
	p := cfg.Provider()
	model = strings.TrimSpace(cfg.Model)
	if model == "" {
		model = p.Model
	}
	model = core.NormalizeModelID(p.BaseURL, model)
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 300 * time.Second
	}

	tlsOpt := llm.TLSOptions{ExtraCAFile: cfg.TLSExtraCA, Insecure: cfg.TLSInsecure}
	c, err := llm.NewWithTLS(p.BaseURL, p.APIKey, model, timeout, tlsOpt)
	if err != nil {
		// TLS 兜底失败不该让整个程序不可用：退回系统默认证书池。
		warn = "TLS 设置生效失败，已使用系统默认证书校验：" + err.Error()
		core.Logf("bootstrap: %s", warn)
		c = llm.New(p.BaseURL, p.APIKey, model, timeout)
	}
	if cfg.TLSInsecure {
		warn = appendWarn(warn, "已跳过 TLS 证书校验：API Key 可能被链路上的中间人窃取（TLSInsecure=true）")
		core.Logf("bootstrap: TLSInsecure=true —— 到 %s 的流量不校验证书", p.BaseURL)
	}
	applyProxy(c, cfg)
	return c, model, warn
}

// applyProxy 在存在代理时启动本机 NTLM 转发桥，让 Go 客户端也能使用
// 当前 Windows 用户的 SSPI 凭据，而不是只支持 Basic/无认证代理。
func applyProxy(c *llm.Client, cfg *core.Config) {
	llm.ResetProxyCache()
	c.SetProxy(cfg.ProxyMode, cfg.ProxyURL)
	if !llm.SystemProxyConfigured() {
		return
	}
	s, err := netbridge.Ensure(llm.ResolveProxyForURL)
	if err != nil {
		core.Logf("bootstrap: 本机代理桥启动失败，回退常规代理：%v", err)
		return
	}
	if s != nil {
		c.SetProxy("manual", s.URL)
		// 更新检查、自检等仍用 http.DefaultClient；也让它们走本机桥，
		// 否则企业 NTLM 代理下只有聊天能用、下载更新却 407。
		if tr, ok := http.DefaultTransport.(*http.Transport); ok {
			if pu, err := url.Parse(s.URL); err == nil {
				tr.Proxy = http.ProxyURL(pu)
			}
		}
	}
}

// appendWarn 把两条提示合并成一句话。
func appendWarn(a, b string) string {
	if a == "" {
		return b
	}
	return a + "；另外，" + b
}

// NewEngine 构造一个可用的引擎（全局默认端点 + 全局默认模型 + 默认工作目录）。
func NewEngine(cfg *core.Config, opt Options) (eng *agent.Engine, model, warn string, err error) {
	return NewEngineFor(cfg, cfg.Provider(), cfg.Model, cfg.WorkDir, opt)
}

// NewEngineFor 按「指定端点 + 指定模型 + 指定工作目录」构造引擎。
// 会话级切换模型、切换工作区都走这里，保证换模型不会串用别的端点地址与密钥。
func NewEngineFor(cfg *core.Config, p core.Provider, model, workDir string, opt Options) (eng *agent.Engine, used string, warn string, err error) {
	if strings.TrimSpace(p.BaseURL) == "" {
		return nil, "", "", fmt.Errorf("尚未配置模型服务地址，请先在设置里选择或填写提供方")
	}
	if strings.TrimSpace(model) == "" && strings.TrimSpace(p.Model) == "" {
		return nil, "", "", fmt.Errorf("模型 ID 为空：请在设置里手填，或从端点 /models 拉取")
	}
	client, used, warn := NewClientFor(cfg, p, model)
	if strings.TrimSpace(workDir) == "" {
		workDir = cfg.WorkDir
	}
	e := agent.New(client, used, workDir)
	e.Temp = cfg.Temperature
	e.MaxTok = cfg.MaxTokens
	e.PriceIn = cfg.PriceIn
	e.PriceOut = cfg.PriceOut
	e.MaxSubTasks = cfg.MaxSubTasks
	// 0 = 不限制轮数（默认）；>0 时撞顶会暂停询问而不是直接失败。
	e.MaxToolRounds = cfg.MaxToolRounds
	e.ContextWindow = cfg.ContextWindow
	e.SubAgentTimeoutSec = cfg.SubAgentTimeoutSec
	e.PlanMode = opt.PlanMode
	// 审批策略：模式 + 规则 + 目录白名单都来自配置，界面改配置即刻生效。
	// 老配置里没有 perm_mode 字段：按旧开关兜底。否则会回落成 ask，
	// 用户明明开着"自动执行"，实际每次写文件都要点确认。
	mode := strings.TrimSpace(cfg.PermMode)
	if mode == "" {
		mode = agent.AutoApproveDecision(cfg.AutoApprove)
	}
	pol := core.NewPolicyFromRules(mode, cfg.PermRules, cfg.PermDirs)
	if opt.PlanMode {
		pol.Mode = core.PermPlan
	}
	e.Policy = pol
	e.VerifyMode = cfg.VerifyMode
	e.Confirm = opt.Confirm
	e.ConfirmDiff = opt.ConfirmDiff
	e.PlanApprove = opt.PlanApprove
	e.Emit = opt.Emit
	return e, used, warn, nil
}
