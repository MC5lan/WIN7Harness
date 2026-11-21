package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var errEmptyDir = errors.New("目录路径为空")

// Version 是应用版本号，同时用于配置迁移判断。
const Version = "1.39.0"

// PresetVersion 是接入预设的版本号。v3 起不再内置任何模型名：
// 应用只认识 OpenAI 兼容协议，模型 ID 由用户填写或从端点 /models 动态拉取。
const PresetVersion = 3

// OpenCodeGoBaseURL 是 OpenCode Go / Go Plus 套餐的 OpenAI 兼容端点。
// 订阅后从 opencode.ai/console 复制 API Key 即可，模型 ID 用控制台里的
// 原始 ID；从 OpenCode 配置里粘来的 "opencode-go/xxx" 会在客户端自动去掉前缀。
const OpenCodeGoBaseURL = "https://opencode.ai/zen/go/v1"

// AppName 用于窗口标题、数据目录、日志等。
const AppName = "WIN7 Harness"

// Provider 描述一个 OpenAI 兼容的模型服务端点。
// 只要对方实现了 POST {BaseURL}/chat/completions 即可接入，
// 因此官方 OpenAI、DeepSeek、Moonshot、通义、Ollama、LM Studio、vLLM、llama.cpp server 全部通用。
type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	// APIKey 只在内存里保存明文；落盘时清空，改为写 APIKeyEnc。
	APIKey string `json:"api_key,omitempty"`
	// APIKeyEnc 是 DPAPI 加密后的 Key（dpapi:v1:base64）；拷贝到
	// 其他 Windows 用户/机器上无法解密，加载时会提示重新填写。
	APIKeyEnc string   `json:"api_key_enc,omitempty"`
	Model     string   `json:"model"`
	Models    []string `json:"models,omitempty"`
	Local     bool     `json:"local,omitempty"`
	NoAuth    bool     `json:"no_auth,omitempty"`
}

// Hook 在工具执行前后触发的本地命令。退出码非 0 时 pre_tool 会阻止本次工具执行。
type Hook struct {
	Event      string `json:"event"`   // pre_tool | post_tool | turn_end
	Match      string `json:"match"`   // 工具名，支持 * 通配与逗号分隔多个
	Command    string `json:"command"` // 要执行的命令行
	TimeoutSec int    `json:"timeout"` // 超时秒数，默认 30
}

// MCPServer 一个 MCP 服务。配了 Command 走本地 stdio，配了 URL 走远程 HTTP/SSE。
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`     // 形如 KEY=VALUE
	URL     string   `json:"url,omitempty"`     // 远程 MCP 地址，如 https://mcp.example.com/mcp
	Headers []string `json:"headers,omitempty"` // 远程请求头，形如 Authorization: Bearer xxx
	Enabled bool     `json:"enabled"`
}

// IsRemote 判断是否为远程 MCP。
func (m MCPServer) IsRemote() bool { return m.URL != "" }

// Target 返回一句人类可读的连接目标。
func (m MCPServer) Target() string {
	if m.URL != "" {
		return m.URL
	}
	return strings.TrimSpace(m.Command + " " + strings.Join(m.Args, " "))
}

// Redacted 返回隐去密钥的头信息，用于日志与界面展示。
func (m MCPServer) Redacted() []string {
	var out []string
	for _, h := range m.Headers {
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			out = append(out, h)
			continue
		}
		if looksSecret(k) {
			out = append(out, strings.TrimSpace(k)+": ***")
			continue
		}
		out = append(out, strings.TrimSpace(k)+": "+strings.TrimSpace(v))
	}
	return out
}

func looksSecret(key string) bool {
	k := strings.ToLower(key)
	for _, w := range []string{"authorization", "token", "key", "secret", "cookie"} {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// Config 是全局配置，落盘为 %APPDATA%\WIN7Harness\config.json。
type Config struct {
	Version       string `json:"version"`
	PresetVersion int    `json:"preset_version"`
	Onboarded     bool   `json:"onboarded"`

	Providers      []Provider `json:"providers"`
	ActiveProvider int        `json:"active_provider"`

	// PortableSecrets 是便携模式下用“U 盘密码”加密的 Key 集合。
	// 格式 pwd:v1:...；标准模式不写这个字段，DPAPI 密文和它互斥。
	PortableSecrets string `json:"portable_secrets,omitempty"`

	// 网络：TLS 兜底与代理。代理默认跟随系统（WinINET）设置。
	TLSInsecure bool   `json:"tls_insecure,omitempty"` // 跳过证书校验，仅排障用
	TLSExtraCA  string `json:"tls_extra_ca,omitempty"` // 补充根证书 PEM 路径
	ProxyMode   string `json:"proxy_mode,omitempty"`   // system | direct | manual
	ProxyURL    string `json:"proxy_url,omitempty"`    // 手动代理，如 http://127.0.0.1:7890

	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	TimeoutSec  int     `json:"timeout_sec"`

	SystemPrompt string `json:"system_prompt"`
	// WorkDir 是默认工作目录：新会话默认用它，单个会话可以在会话内改成别的目录。
	WorkDir string `json:"work_dir"`
	// RecentDirs 是最近用过的工作区，供会话内切换时一键选回。
	RecentDirs []string `json:"recent_dirs,omitempty"`

	Theme     string `json:"theme"`      // dark | light | hc（高对比）
	FontScale int    `json:"font_scale"` // 内容文字缩放百分比 80~200
	// 面板宽度（逻辑像素，96dpi）：由分隔条拖拽决定并记忆。
	// 0 表示用内置默认值（侧栏 232 / 检查器 272）。
	SideW int `json:"side_w,omitempty"`
	InspW int `json:"insp_w,omitempty"`
	// ShowInsp 记忆检查器是否展开：关掉的人重启后不该又冒出来，
	// 开着的人也不该每次都要重新点一遍。
	ShowInsp    bool   `json:"show_insp,omitempty"`
	AutoApprove bool   `json:"auto_approve"`  // 自动执行低危命令（写文件/读文件）
	SendOnEnter bool   `json:"send_on_enter"` // Enter 发送，Shift+Enter 换行
	DiffPreview string `json:"diff_preview"`  // 写盘前差异预览：off | large | always
	// RenderMode 渲染后端：空/canvas = 矢量抗锯齿（默认）；gdi = 兼容渲染。
	// Win7 老显卡、16bpp、远程桌面下矢量光栅可能偏慢，GDI 更稳更快。
	// 改动需要重启生效（Canvas 在窗口创建时选择后端）。
	RenderMode string `json:"render_mode,omitempty"`
	// KeyBindings 是"动作 -> 组合键"的可覆盖映射，如 {"historySearch":"ctrl+r"}。
	// 缺失项由 DefaultConfig 兜底，旧配置文件无需修改即可用。
	KeyBindings  map[string]string `json:"keybindings,omitempty"`
	ShowThinking bool              `json:"show_thinking"`
	MaxSubTasks  int               `json:"max_sub_tasks"` // 批量派发子任务上限 1..8，默认 3
	ReasoningOn  bool              `json:"reasoning_on"`
	// LowMemory 低内存模式：为 32 位进程与小内存机器收紧上下文预算、工具输出、
	// 索引规模与图片尺寸，代价是能读的内容变少（详见 core.MemProfile）。
	LowMemory bool `json:"low_memory,omitempty"`
	// VerifyMode 自动验证：改完代码后自动跑构建 + 测试，失败把摘要交回模型继续修。
	// off | auto，空值表示关闭（用 /verify on 打开）。
	VerifyMode string `json:"verify_mode,omitempty"`
	// CostBoard 在状态栏显示实时成本与缓存命中率（默认开启，可在设置里关掉）。
	CostBoard bool `json:"cost_board,omitempty"`
	// RouterOn 模型路由：上下文压缩与只读子代理改用 FastModel，主对话不变。
	// 默认关闭——省钱的前提是"快速模型够用"，这个判断交给用户，不替他做。
	RouterOn bool `json:"router_on,omitempty"`
	// FastModel 轻任务模型；为空时即便 RouterOn 打开也全部走主模型。
	FastModel string `json:"fast_model,omitempty"`
	// —— 手机远程指挥台（v1.27）——
	// 默认关闭：这不是"默认该开"的功能。开了它，同一局域网内任何设备
	// 都有可能摸到这台机器上的对话，必须由用户自己决定打开。
	RemoteEnabled bool `json:"remote_enabled,omitempty"`
	RemotePort    int  `json:"remote_port,omitempty"` // 默认 8765
	// RemoteLAN 监听所有网卡（手机可访问）；false 时只监听 127.0.0.1，
	// 相当于"本机浏览器预览"，不给局域网留口子。
	RemoteLAN bool `json:"remote_lan,omitempty"`
	// RemoteHTTPS（v1.28）用自签证书走 TLS。局域网里有别人的 Wi-Fi 时
	// 开着更稳：配对码和指令不再明文过网。
	// 代价是手机第一次访问会多一次"继续访问"的确认，所以默认关闭。
	RemoteHTTPS bool `json:"remote_https,omitempty"`
	// RemoteApproval（v1.28）把需要确认的高危操作同步推到手机，
	// 手机点了就算答复。默认开启：开了指挥台的人基本都在等这个。
	RemoteApproval bool `json:"remote_approval,omitempty"`
	// RemoteApprovalSet 区分"没配置过"与"配置成 false"。
	RemoteApprovalSet bool `json:"remote_approval_set,omitempty"`

	// —— 更新检查（v1.27）——
	// UpdateURL 是更新清单地址（JSON）。为空则更新检查整体不可用，
	// 不做任何默认联网行为。
	UpdateURL string `json:"update_url,omitempty"`
	// CheckUpdateOnStart 启动时静默查一次；只提示，不自动下载替换。
	CheckUpdateOnStart bool `json:"check_update_on_start,omitempty"`

	// NoFoldTools 关闭"工具卡按组合并"。默认零值即开启，故用反向字段存盘：
	// 老配置里没有这个键，反序列化后仍是 false，正好保持默认开启，
	// 不会像正向 bool 那样把"默认 true"读成"用户关掉了"。
	NoFoldTools bool `json:"no_fold_tools,omitempty"`

	// SubTaskMaxChars 单个子任务回灌给主对话的字数上限，0 表示默认 6000。
	// 子代理能读一万行日志，主对话只需要它的判断，不设上限等于让一次派发吃掉半屏上下文。
	SubTaskMaxChars int `json:"sub_task_max_chars,omitempty"`

	// 本地命令钩子：工具执行前后、每轮结束时触发，用于自动格式化、检查、通知。
	Hooks []Hook `json:"hooks,omitempty"`
	// 价格（每百万 token，美元），仅用于本地估算，不联网查询。
	PriceIn  float64 `json:"price_in"`
	PriceOut float64 `json:"price_out"`

	// 预算护栏：0 表示不限制。会话累计用量触及上限时自动停止继续调用模型，
	// 只提示、不改配置、不扣费，用户可显式继续。
	MaxCost      float64 `json:"max_cost"`      // 单会话累计花费上限（美元）
	BudgetTokens int     `json:"budget_tokens"` // 单会话累计 token 上限
	BudgetWarn   bool    `json:"budget_warn"`   // 达到 80% 时是否提示
	// MaxToolRounds 单次请求内的工具循环轮数上限。0（默认）表示不限制：
	// 大重构动辄上百次工具调用，用轮数猜任务大小只会把正常任务掐断。
	// 真正的护栏是上面的花费/token 上限，以及"同一错误反复出现"的自动拦截。
	// 设成正数时，撞顶不会失败，而是暂停问用户要不要继续（同意则翻倍）。
	MaxToolRounds int `json:"max_tool_rounds,omitempty"`
	// ContextWindow 模型的上下文窗口（token）。自动压缩阈值按它算：
	// 换了窗口更大的模型，就不该还沿用旧模型的小阈值。0 = 按 128k 估算。
	ContextWindow int `json:"context_window,omitempty"`
	// ToolProfile 控制每轮发给模型的工具定义：auto（默认，按窗口自动）/ full / core / plan。
	// plan 模式另外会强制只读，和这里的值无关。
	// 工具太多会挤占上下文，也会诱导模型调用用不上的工具。
	ToolProfile string `json:"tool_profile,omitempty"`
	// DisabledSkills 禁用指定技能（内置技能也可禁用），按名称不区分大小写。
	DisabledSkills []string `json:"disabled_skills,omitempty"`
	// PromptCache 控制 Anthropic 风格 cache_control：auto（默认，只对已知网关）/ off / anthropic。
	PromptCache string `json:"prompt_cache,omitempty"`
	// SubAgentTimeoutSec 单个子代理任务的超时（秒）。0 = 默认 20 分钟。
	// 老值硬编码 8 分钟，分析一个大模块 + 跑一次构建必然超时。
	SubAgentTimeoutSec int `json:"subagent_timeout_sec,omitempty"`
	// MCP 服务列表，启动时按需连接，工具自动并入工具集。
	MCPServers []MCPServer `json:"mcp_servers,omitempty"`

	// PermMode 权限模式：ask（默认，写操作询问）/ auto（自动）/ plan（只读）。
	PermMode string `json:"perm_mode,omitempty"`
	// PermRules 权限规则，形如 "allow:run_command:go *"、"deny:run_command:rm -rf *"。
	PermRules []string `json:"perm_rules,omitempty"`
	// PermDirs 允许写入的额外目录（工作区之外）。
	PermDirs []string `json:"perm_dirs,omitempty"`
}

var (
	cfgMu   sync.RWMutex
	current *Config

	// 便携模式的解锁状态，和配置一起由 cfgMu 保护。
	// portablePassword 只在内存里；程序退出即消失。
	portablePassword string
	portableLocked   bool
)

// DataDir 返回漫游配置目录（%APPDATA%\WIN7Harness）。
// 只放 config/skills/commands 等小文件；会话、索引、日志、快照、更新包
// 放 LocalDataDir，避免域漫游配置在登录/注销时同步几百 MB。
func DataDir() string {
	if d := os.Getenv("WIN7HARNESS_HOME"); d != "" {
		return d
	}
	// 便携模式：数据放在 exe 同级 data 目录，U 盘拷贝即走即用
	if PortableMode() {
		return filepath.Join(ExeDir(), "data")
	}
	if base, err := os.UserConfigDir(); err == nil && base != "" {
		return filepath.Join(base, "WIN7Harness")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".win7harness")
}

// PortableMode 判断是否运行在便携（绿色）模式：优先看环境变量，其次看 exe 同级标记文件。
func PortableMode() bool {
	if v := os.Getenv("WIN7HARNESS_PORTABLE"); v == "1" || v == "true" {
		return true
	}
	dir := ExeDir()
	found := false
	for _, name := range []string{"portable.flag", "portable.txt"} {
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && !st.IsDir() {
			found = true
			break
		}
	}
	if !found {
		return false
	}
	// 介质可能写保护（只读 U 盘、光盘），不能写就回退到用户配置目录
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		return false
	}
	probe := filepath.Join(data, ".writable")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(probe)
	return true
}

// ExeDir 返回可执行文件所在目录，便携模式下作为工作目录默认值。
func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// LocalDataDir 返回本机缓存目录（%LOCALAPPDATA%\WIN7Harness）。
// 便携模式/测试 home 下与 DataDir 相同，保证绿色版和单测行为不变。
func LocalDataDir() string {
	if d := os.Getenv("WIN7HARNESS_HOME"); d != "" {
		return d
	}
	if PortableMode() {
		return filepath.Join(ExeDir(), "data")
	}
	if base, err := os.UserCacheDir(); err == nil && base != "" {
		return filepath.Join(base, "WIN7Harness")
	}
	return DataDir()
}

// SessionsDir 会话持久化目录（本机，不进漫游配置）。
func SessionsDir() string { return filepath.Join(LocalDataDir(), "sessions") }

// LogPath 日志文件路径（本机）。
func LogPath() string { return filepath.Join(LocalDataDir(), "harness.log") }

// ConfigPath 配置文件路径。
func ConfigPath() string { return filepath.Join(DataDir(), "config.json") }

// DefaultConfig 返回带预置端点的默认配置。
func DefaultConfig() *Config {
	wd := ""
	if st, err := os.Stat(filepath.Join(ExeDir(), "workspace")); err == nil && st.IsDir() {
		wd = filepath.Join(ExeDir(), "workspace")
	} else {
		home, _ := os.UserHomeDir()
		wd = filepath.Join(home, "WIN7Harness-Workspace")
	}
	providers := PresetProviders()
	return &Config{
		Version:       Version,
		PresetVersion: PresetVersion,
		Onboarded:     false,
		Providers:     providers,
		Model:         providers[0].Model,
		Temperature:   0.3,
		MaxTokens:     8192,
		TimeoutSec:    300,
		SystemPrompt:  "",
		WorkDir:       wd,
		Theme:         "light",
		FontScale:     100,
		AutoApprove:   true,
		SendOnEnter:   true,
		CostBoard:     true,
		DiffPreview:   "large",
		// 新配置默认开启自动验证：改完代码先构建+测试，失败回灌给模型继续修。
		// 老配置没有该字段时仍按 off 处理，用户可在设置 → 外观里手动打开。
		VerifyMode:   "auto",
		ToolProfile:  "auto",
		PromptCache:  "auto",
		KeyBindings:  DefaultKeyBindings(),
		ShowThinking: true,
		// 8 千字符约等于 2 千 token，够放结论与关键证据，又不至于挤掉主对话的上下文
		SubTaskMaxChars: 8000,
		ReasoningOn:     false,
	}
}

// PresetProviders 只保留“接入方式”，不内置任何厂商或模型名。
//
// 设计约束（v1.32）：
//   - 应用只承诺兼容 OpenAI Chat Completions 协议；
//   - 模型 ID 一律由用户填写，或点“拉取模型”从端点 /models 动态获取；
//   - APIKey 一律留空，绝不内置任何真实密钥。
func PresetProviders() []Provider {
	return []Provider{
		{
			ID:      "opencode-go",
			Name:    "OpenCode Go / Go Plus",
			BaseURL: OpenCodeGoBaseURL,
		},
		{
			ID:   "custom",
			Name: "自定义 OpenAI 兼容端点",
		},
		{
			ID:      "local",
			Name:    "本地模型（Ollama / LM Studio / vLLM）",
			BaseURL: "http://127.0.0.1:11434/v1",
			Local:   true,
			NoAuth:  true,
		},
	}
}

// IsOpenCodeGo 判断端点是否 OpenCode Go 套餐（含新旧两个控制台域名）。
func IsOpenCodeGo(baseURL string) bool {
	u := strings.ToLower(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	return strings.Contains(u, "opencode.ai/zen/go") || strings.Contains(u, "opencode.ai/inference/go")
}

// NormalizeBaseURL 做一层协议兼容修剪：
//   - 去掉末尾斜杠；
//   - OpenCode Go 只填到 /zen/go 或 /inference/go 时补 /v1；
//   - 用户直接粘完整 /chat/completions 地址时保留（客户端会原样使用）。
func NormalizeBaseURL(raw string) string {
	u := strings.TrimRight(strings.TrimSpace(raw), "/")
	if u == "" {
		return ""
	}
	lower := strings.ToLower(u)
	if IsOpenCodeGo(u) && !strings.HasSuffix(lower, "/v1") && !strings.HasSuffix(lower, "/chat/completions") {
		u += "/v1"
	}
	return u
}

// NormalizeModelID 让从 OpenCode 配置里粘来的 "opencode-go/模型ID" 也能直接用。
// 只对 OpenCode Go 端点生效，避免误伤自定义网关的命名空间。
func NormalizeModelID(baseURL, model string) string {
	m := strings.TrimSpace(model)
	if m == "" || !IsOpenCodeGo(baseURL) {
		return m
	}
	if strings.HasPrefix(strings.ToLower(m), "opencode-go/") {
		return strings.TrimSpace(m[len("opencode-go/"):])
	}
	return m
}

// Load 读取配置，不存在则返回默认值并落盘。
// lastConfigErr 最近一次配置加载的异常说明（v1.29）。正常加载时会被清空。
var lastConfigErr string

// ConfigLoadError 返回配置加载异常说明；空串表示正常。
func ConfigLoadError() string {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	return lastConfigErr
}

// TakeConfigLoadError 读走并清空异常说明（界面播报一次即可）。
func TakeConfigLoadError() string {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	msg := lastConfigErr
	lastConfigErr = ""
	return msg
}

func Load() *Config {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	c := DefaultConfig()
	data, err := os.ReadFile(ConfigPath())
	if err == nil && len(data) > 0 {
		// Win7 记事本另存为 UTF-8 会带 BOM；JSON 解析器不认。
		// 不剥掉就会把整份配置判成"损坏"并回退默认，用户 Key/模型全丢。
		data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
		// 严格路径：直接解析并覆盖 c，绝不遮蔽 c——
		// 之前的写法 c := &Config{} 只在局部可见，函数继续用外层 DefaultConfig()，
		// 导致所有用户保存的 Key/模型名/地址静默丢失（只在运行态生效，不落盘）。
		if jerr := json.Unmarshal(data, c); jerr == nil {
			c = mergeConfig(DefaultConfig(), c)
			lastConfigErr = ""
		} else if tc, bad, terr := tolerantLoad(data); terr == nil {
			// 容错解析：只有坏的字段回退默认，其余设置保留
			c = mergeConfig(DefaultConfig(), tc)
			if len(bad) > 0 {
				lastConfigErr = "配置文件有坏字段已按默认重置：" + strings.Join(bad, "、")
				// 把修好的配置落盘：用户保留好的设置，不会每次启动都再警告一次
				if werr := c.saveLocked(); werr != nil {
					Logf("config: 修复后的配置落盘失败（%v），本次继续运行", werr)
				} else {
					lastConfigErr += "（已修好并写回，下次启动不再提示）"
					Logf("config: 容错解析生效，坏字段已按默认重置：%s", strings.Join(bad, "、"))
				}
			} else {
				lastConfigErr = ""
			}
		} else {
			// v1.29：以前这里 json 解析失败就当没配过，用户的所有设置凭空消失且毫无提示。
			// 现在先备份再按默认启动，并把原因留给界面播报。
			if bak, qerr := Quarantine(ConfigPath()); qerr == nil {
				lastConfigErr = "配置文件损坏，已备份为 " + filepath.Base(bak) + "，本次按默认配置启动"
			} else {
				lastConfigErr = "配置文件损坏且无法备份：" + terr.Error()
			}
		}
	}
	c.loadProviderKeys()
	c.Version = Version
	c.Normalize()
	// 老版本把所有数据放在漫游 %APPDATA%；首次升级时把大目录迁到本机。
	MigrateLocalData()
	current = c
	// 内存档位是全局运行时状态，配置一加载就同步，保证所有入口行为一致。
	SetLowMemory(c.LowMemory)
	return c
}

// mergeConfig 用已存配置覆盖默认值，同时补齐新增的接入预设。
//
// v1.32 起不再揣测用户的模型名：已存配置里的 provider / model / Key 原样保留，
// 只把新增的内置“接入方式”（OpenCode Go、自定义、本地）按 ID 补进去。
// 这样既不会把用户配好的模型改成我们猜的名字，也不会把旧 Key 覆盖掉。
func mergeConfig(def *Config, saved *Config) *Config {
	if len(saved.Providers) == 0 {
		saved.Providers = def.Providers
		saved.PresetVersion = def.PresetVersion
		if strings.TrimSpace(saved.Model) == "" {
			saved.Model = def.Model
		}
		return saved
	}

	byID := map[string]Provider{}
	for _, p := range saved.Providers {
		byID[p.ID] = p
	}
	merged := append([]Provider{}, saved.Providers...)
	for _, p := range def.Providers {
		if _, ok := byID[p.ID]; !ok {
			merged = append(merged, p)
		}
	}
	saved.Providers = merged
	saved.PresetVersion = def.PresetVersion
	return saved
}

// Normalize 修正非法或空值，保证后续代码不会 nil/越界。
func (c *Config) Normalize() {
	if c.DiffPreview == "" {
		c.DiffPreview = "large"
	}
	for i := range c.Hooks {
		if c.Hooks[i].TimeoutSec <= 0 {
			c.Hooks[i].TimeoutSec = 30
		}
		if c.Hooks[i].TimeoutSec > 600 {
			c.Hooks[i].TimeoutSec = 600
		}
		if c.Hooks[i].Event == "" {
			c.Hooks[i].Event = "pre_tool"
		}
		if c.Hooks[i].Match == "" {
			c.Hooks[i].Match = "*"
		}
	}
	for i := range c.MCPServers {
		if c.MCPServers[i].Name == "" {
			c.MCPServers[i].Name = "mcp"
		}
	}
	if c.Providers == nil || len(c.Providers) == 0 {
		c.Providers = PresetProviders()
	}
	if c.ActiveProvider < 0 || c.ActiveProvider >= len(c.Providers) {
		c.ActiveProvider = 0
	}
	for i := range c.Providers {
		p := &c.Providers[i]
		p.BaseURL = NormalizeBaseURL(p.BaseURL)
		if p.BaseURL != "" && !strings.HasPrefix(p.BaseURL, "http") {
			p.BaseURL = "http://" + p.BaseURL
		}
		if p.Name == "" {
			p.Name = p.ID
		}
		p.Model = NormalizeModelID(p.BaseURL, p.Model)
	}
	c.Model = NormalizeModelID(c.Providers[c.ActiveProvider].BaseURL, c.Model)
	if c.Model == "" {
		c.Model = c.Providers[c.ActiveProvider].Model
	}
	if c.MaxTokens <= 0 {
		c.MaxTokens = 4096
	}
	if c.MaxSubTasks <= 0 {
		c.MaxSubTasks = 3
	}
	if c.MaxSubTasks > 8 {
		c.MaxSubTasks = 8
	}
	if c.MaxCost < 0 {
		c.MaxCost = 0
	}
	if c.BudgetTokens < 0 {
		c.BudgetTokens = 0
	}
	if c.MaxToolRounds < 0 {
		c.MaxToolRounds = 0
	}
	if c.ContextWindow < 0 {
		c.ContextWindow = 0
	}
	switch strings.ToLower(strings.TrimSpace(c.ToolProfile)) {
	case "full", "core", "plan":
		c.ToolProfile = strings.ToLower(strings.TrimSpace(c.ToolProfile))
	default:
		c.ToolProfile = "auto"
	}
	switch strings.ToLower(strings.TrimSpace(c.PromptCache)) {
	case "off", "anthropic", "auto":
		c.PromptCache = strings.ToLower(strings.TrimSpace(c.PromptCache))
	default:
		c.PromptCache = "auto"
	}
	if c.SubAgentTimeoutSec < 0 {
		c.SubAgentTimeoutSec = 0
	}
	// 禁用技能列表去空、去重（保留大小写，匹配时忽略大小写）。
	if len(c.DisabledSkills) > 0 {
		seen := map[string]bool{}
		out := c.DisabledSkills[:0]
		for _, n := range c.DisabledSkills {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			key := strings.ToLower(n)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, n)
		}
		c.DisabledSkills = out
	}
	if c.TimeoutSec <= 0 {
		c.TimeoutSec = 300
	}
	if c.Temperature < 0 {
		c.Temperature = 0
	}
	if c.FontScale < 80 || c.FontScale > 200 {
		c.FontScale = 100
	}
	// 面板宽度超范围时退回默认：配置被手改坏也不会让界面塌成一条缝。
	if c.SideW < 160 || c.SideW > 720 {
		c.SideW = 0
	}
	if c.InspW < 180 || c.InspW > 900 {
		c.InspW = 0
	}
	switch strings.ToLower(strings.TrimSpace(c.Theme)) {
	case "light":
		c.Theme = "light"
	case "hc", "high-contrast", "highcontrast", "contrast":
		c.Theme = "hc"
	default:
		c.Theme = "dark"
	}
	if c.RemotePort < 1024 || c.RemotePort > 65535 {
		c.RemotePort = 8765
	}
	if strings.TrimSpace(c.WorkDir) == "" {
		home, _ := os.UserHomeDir()
		c.WorkDir = filepath.Join(home, "WIN7Harness-Workspace")
	}
	if err := ValidateWorkDir(c.WorkDir); err != nil {
		home, _ := os.UserHomeDir()
		c.WorkDir = filepath.Join(home, "WIN7Harness-Workspace")
		SetStartupWarning("原工作区不可用（" + err.Error() + "），已改回 " + c.WorkDir)
	}
}

// Save 原子写盘。
func (c *Config) Save() error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	return c.saveLocked()
}

// loadProviderKeys 按运行模式还原 API Key。
//
//   - 标准模式：DPAPI 密文 -> 内存明文；
//   - 便携模式且存在 portable_secrets：先保持锁定，等用户输入 U 盘密码；
//   - 便携模式但没有 portable_secrets：兼容旧版 DPAPI 配置，能解就解。
func (c *Config) loadProviderKeys() {
	portableLocked = false
	portablePassword = ""
	if PortableMode() && strings.TrimSpace(c.PortableSecrets) != "" {
		for i := range c.Providers {
			c.Providers[i].APIKey = ""
		}
		portableLocked = true
		return
	}
	if !PortableMode() && strings.TrimSpace(c.PortableSecrets) != "" {
		SetStartupWarning("配置里有 U 盘密码加密的 API Key，但当前不是便携模式；请用 U 盘启动，或在设置里重新填写 Key")
	}
	c.decryptProviderKeys()
}

// decryptProviderKeys 把 DPAPI 密文还原到内存里的 APIKey。
// 解密失败（配置来自其他用户/机器）时清掉不可用的密文，让用户重新填。
func (c *Config) decryptProviderKeys() {
	for i := range c.Providers {
		p := &c.Providers[i]
		if strings.TrimSpace(p.APIKey) != "" || p.APIKeyEnc == "" {
			continue
		}
		key, err := openSecret(p.APIKeyEnc)
		if err != nil {
			Logf("config: provider %s 的 API Key 无法解密：%v", p.ID, err)
			SetStartupWarning("配置中的 API Key 无法解密（可能来自其他 Windows 用户/机器），请在设置里重新填写")
			p.APIKeyEnc = ""
			continue
		}
		p.APIKey = key
	}
}

// PortableSecretsPresent 表示 config.json 里有 U 盘密码加密的 Key。
func PortableSecretsPresent() bool {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return current != nil && strings.TrimSpace(current.PortableSecrets) != ""
}

// PortableSecretsLocked 表示有 U 盘密钥，但本次启动还没输入密码。
func PortableSecretsLocked() bool {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return portableLocked
}

// PortablePasswordSet 表示本次运行已经拿到 U 盘密码（可安全落盘）。
func PortablePasswordSet() bool {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return portablePassword != ""
}

// UnlockPortableSecrets 用用户输入的密码解开 portable_secrets，并写回内存 Provider。
// 密码错误不会清空密文，用户可以继续重试。
func UnlockPortableSecrets(password string) error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if current == nil {
		return errors.New("配置尚未加载")
	}
	if strings.TrimSpace(current.PortableSecrets) == "" {
		return errors.New("当前配置没有 U 盘密钥")
	}
	plain, err := openPortableSecrets(current.PortableSecrets, password)
	if err != nil {
		return err
	}
	var payload portableSecretsPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return fmt.Errorf("U 盘密钥内容损坏：%w", err)
	}
	for i := range current.Providers {
		if k, ok := payload.Keys[current.Providers[i].ID]; ok {
			current.Providers[i].APIKey = k
		}
	}
	portablePassword = password
	portableLocked = false
	return nil
}

// SetPortablePassword 记住本次运行的 U 盘密码；随后 Save 会把内存里的 Key 加密落盘。
func SetPortablePassword(password string) error {
	if len([]rune(password)) < 8 {
		return errors.New("U 盘密码至少 8 个字符")
	}
	cfgMu.Lock()
	defer cfgMu.Unlock()
	portablePassword = password
	portableLocked = false
	return nil
}

// ClearPortableSecrets 用于“忘记密码”：清掉密文和内存里的 Key，让用户重新填写。
// 调用方随后应 Save 一次。
func ClearPortableSecrets() error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if current == nil {
		return errors.New("配置尚未加载")
	}
	current.PortableSecrets = ""
	for i := range current.Providers {
		current.Providers[i].APIKey = ""
		current.Providers[i].APIKeyEnc = ""
	}
	portablePassword = ""
	portableLocked = false
	return nil
}

// portableSecretsPayload 是加密前的明文结构。用 provider ID 做 key，
// 这样以后增删接入方式不会因为顺序变化而串 Key。
type portableSecretsPayload struct {
	Version int               `json:"v"`
	Keys    map[string]string `json:"keys"`
}

// forDisk 返回落盘副本：明文 APIKey 一律不落盘。
// 便携模式用 U 盘密码加密；标准模式用 DPAPI。
func (c *Config) forDisk() (*Config, error) {
	cp := *c
	cp.Providers = make([]Provider, len(c.Providers))
	copy(cp.Providers, c.Providers)

	if PortableMode() {
		keys := map[string]string{}
		for _, p := range c.Providers {
			if strings.TrimSpace(p.APIKey) != "" {
				keys[p.ID] = p.APIKey
			}
		}
		if portableLocked && strings.TrimSpace(c.PortableSecrets) != "" {
			// 没解锁时不能改密文，也不能把“内存里没有 Key”误判成用户删除了 Key。
			cp.PortableSecrets = c.PortableSecrets
		} else if len(keys) > 0 {
			if portablePassword == "" {
				return nil, ErrPortablePasswordRequired
			}
			payload, err := json.Marshal(portableSecretsPayload{Version: 1, Keys: keys})
			if err != nil {
				return nil, err
			}
			enc, err := sealPortableSecrets(payload, portablePassword)
			if err != nil {
				return nil, fmt.Errorf("加密 U 盘 API Key 失败：%w", err)
			}
			cp.PortableSecrets = enc
		} else {
			cp.PortableSecrets = ""
		}
		for i := range cp.Providers {
			cp.Providers[i].APIKey = ""
			cp.Providers[i].APIKeyEnc = ""
		}
		return &cp, nil
	}

	for i := range cp.Providers {
		p := &cp.Providers[i]
		if p.APIKey == "" {
			continue
		}
		enc, err := sealSecret(p.APIKey)
		if err != nil {
			return nil, fmt.Errorf("加密 provider %s 的 API Key 失败：%w", p.ID, err)
		}
		p.APIKey = ""
		p.APIKeyEnc = enc
	}
	return &cp, nil
}

// saveLocked 原子写配置（调用方需持 cfgMu）。
func (c *Config) saveLocked() error {
	if err := os.MkdirAll(DataDir(), 0o755); err != nil {
		return err
	}
	cp, err := c.forDisk()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cp, "  ", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(ConfigPath(), data, 0o644)
}

// Get 返回当前进程内配置（只读用途，调用方不要修改其字段）。
// HooksSnapshot 返回钩子配置的副本：界面线程可能正在增删钩子，
// 工具执行线程若直接引用切片会与 append 发生数据竞争。
func HooksSnapshot() []Hook {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	if current == nil {
		return nil
	}
	out := make([]Hook, len(current.Hooks))
	copy(out, current.Hooks)
	return out
}

func Get() *Config {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	if current == nil {
		return DefaultConfig()
	}
	return current
}

// ProviderByID 按 ID 查端点，供会话级模型覆盖使用（不同模型可能落在不同端点上）。
func (c *Config) ProviderByID(id string) (Provider, bool) {
	if strings.TrimSpace(id) == "" {
		return Provider{}, false
	}
	for _, p := range c.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// TouchRecentDir 把目录提到最近使用列表头部（上限 8 条，去重）。
func (c *Config) TouchRecentDir(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	out := []string{dir}
	for _, d := range c.RecentDirs {
		if !strings.EqualFold(d, dir) {
			out = append(out, d)
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	c.RecentDirs = out
}

// Provider 返回当前生效端点。
func (c *Config) Provider() Provider {
	if c.ActiveProvider < 0 || c.ActiveProvider >= len(c.Providers) {
		c.Normalize()
	}
	return c.Providers[c.ActiveProvider]
}

// EnsureDirs 创建必要目录（数据目录、会话目录、工作目录）。
func EnsureDirs(c *Config) error {
	if strings.TrimSpace(c.WorkDir) != "" {
		if err := ValidateWorkDir(c.WorkDir); err != nil {
			return err
		}
	}
	for _, d := range []string{DataDir(), SessionsDir(), c.WorkDir} {
		if d == "" {
			continue
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// EnsureWorkDir 创建工作目录（含多级）。
func EnsureWorkDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errEmptyDir
	}
	if err := ValidateWorkDir(dir); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}

// ValidateWorkDir 拒绝把程序目录/数据目录当工作区。
// 便携模式最容易踩：用户可能把 U 盘根目录或 X:\WIN7Harness 直接选成工作区，
// 模型随后就可能改到 exe、config 或会话文件。
func ValidateWorkDir(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errEmptyDir
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	protected := []struct {
		name string
		path string
	}{
		{"程序目录", ExeDir()},
		{"配置目录", DataDir()},
		{"本地数据目录", LocalDataDir()},
		{"会话目录", SessionsDir()},
	}
	for _, p := range protected {
		if strings.TrimSpace(p.path) == "" {
			continue
		}
		pa, err := filepath.Abs(p.path)
		if err != nil {
			continue
		}
		if samePath(abs, pa) {
			return fmt.Errorf("工作区不能是%s：%s", p.name, p.path)
		}
		rel, err := filepath.Rel(pa, abs)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("工作区不能放在%s里面：%s", p.name, p.path)
		}
	}
	return nil
}

// Is386 判断当前二进制是否为 32 位构建。
func Is386() bool { return strconv.IntSize == 32 }
