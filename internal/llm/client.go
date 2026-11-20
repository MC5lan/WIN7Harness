// Package llm 提供与 OpenAI Chat Completions 协议完全兼容的流式客户端。
// 任何实现了 /v1/chat/completions 的服务（DeepSeek、Moonshot、通义、GLM、
// SiliconFlow、Ollama、LM Studio、vLLM、llama.cpp server、one-api 等）都可直接使用。
package llm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"win7harness/internal/core"
)

// Message 对话消息。
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Reasoning  string     `json:"reasoning_content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// Parts 多模态内容段（图片等）。为空时消息按纯文本发送；
	// 序列化逻辑见 multimodal.go 的 MarshalJSON。
	Parts []Part `json:"-"`
	// 仅本地 UI 使用，不序列化
	Hidden bool `json:"-"`
}

// ToolCall 模型发起的工具调用。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall 工具调用内容。
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef 工具定义（JSON Schema）。
type ToolDef struct {
	Type     string      `json:"type"`
	Function ToolFuncDef `json:"function"`
}

// ToolFuncDef 工具函数描述。
type ToolFuncDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Usage token 用量。
//
// CachedTokens / CacheWriteTokens 是提示缓存的命中与写入学段：
// 各家字段名不同（OpenAI 的 prompt_tokens_details.cached_tokens、
// DeepSeek 的 prompt_cache_hit_tokens、Anthropic 的 cache_read_input_tokens），
// 统一在 usageUnmarshal 里归一，上层只认这两个值。
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// Client 是 OpenAI 兼容客户端。
type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	Org     string
	// SessionID 是 OpenCode Go 要求的稳定会话标识（x-opencode-session）。
	// 每个会话一个，用于上游路由与提示缓存；其他端点忽略此字段。
	SessionID string
	Timeout   time.Duration

	// RetryBase 是重试退避的基础间隔（0 用默认 1s），MaxRetries 是重试次数
	// （0 用默认 3，负数表示关闭重试）。测试里调小以加快速度。
	RetryBase  time.Duration
	MaxRetries int

	tr   *http.Transport
	http *http.Client
}

func (c *Client) retryBase() time.Duration {
	if c.RetryBase > 0 {
		return c.RetryBase
	}
	return defaultRetryBase
}

func (c *Client) maxRetries() int {
	switch {
	case c.MaxRetries < 0:
		return 0
	case c.MaxRetries == 0:
		return 3
	}
	return c.MaxRetries
}

// New 创建客户端（TLS 使用系统池 + 内置根证书兜底）。
func New(baseURL, apiKey, model string, timeout time.Duration) *Client {
	c, err := NewWithTLS(baseURL, apiKey, model, timeout, TLSOptions{})
	if err != nil {
		// 内置兜底配置理论上不会失败，真失败时退回无自定义 TLS 的客户端，
		// 报错留给实际请求阶段（错误信息更贴近用户操作）。
		c = &Client{}
		c.BaseURL = core.NormalizeBaseURL(baseURL)
		c.APIKey = strings.TrimSpace(apiKey)
		c.Model = core.NormalizeModelID(c.BaseURL, model)
		c.SessionID = newSessionID()
		c.Timeout = timeout
		if c.Timeout <= 0 {
			c.Timeout = 300 * time.Second
		}
		c.tr = newTransport(nil)
		c.http = &http.Client{Transport: c.tr}
	}
	return c
}

// NewWithTLS 创建客户端并显式指定 TLS 设置。
func NewWithTLS(baseURL, apiKey, model string, timeout time.Duration, tlsOpt TLSOptions) (*Client, error) {
	baseURL = core.NormalizeBaseURL(baseURL)
	model = core.NormalizeModelID(baseURL, model)
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	tlsCfg, err := NewTLSConfig(tlsOpt)
	if err != nil {
		return nil, err
	}
	c := &Client{
		BaseURL:   baseURL,
		APIKey:    strings.TrimSpace(apiKey),
		Model:     model,
		SessionID: newSessionID(),
		Timeout:   timeout,
		tr:        newTransport(tlsCfg),
	}
	c.init()
	return c, nil
}

func (c *Client) init() {
	if c.tr == nil {
		// 兜底分支（NewTLSConfig 理论上不会失败）：仍然给一个完整 TLS 配置，
		// 否则就是“裸 TLS + 无下限 + 无内置根”的客户端——
		// 与本设计的兜底意图直接相反。
		pool, perr := x509.SystemCertPool()
		if perr != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		_ = addPEM(pool, bundledRoots)
		c.tr = newTransport(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})
	}
	c.http = &http.Client{Transport: c.tr}
}

// newTransport 统一的 HTTP 传输层：系统代理 + TLS 配置。
// 注意：不在此开启 TLS 会话缓存（ClientSessionCache）——经拦截型网关实测
// 会话票据会让网关在聊天渠道直接 401；且 LLM 串行调用几乎用不上握手复用。
func newTransport(tlsCfg *tls.Config) *http.Transport {
	return &http.Transport{
		Proxy: systemProxy(),
		DialContext: (&net.Dialer{
			Timeout:   20 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   20 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// 不强开 HTTP/2：实测（2026-11，v1.30.0 自检）部分 OpenAI 兼容网关
		// 收到 h2 请求会在入口网关直接 401（"governor"），而 h1 一切正常。
		// 退回默认行为 = h1；Safe for Win7 且与 v1.29.x 行为一致。
		// 响应头 30s 没回来就是上游/网络已经死了，别干等 300s 预算。
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    false,
		TLSClientConfig:       tlsCfg,
	}
}

// Handlers 流式回调。所有回调都在调用 Chat 的 goroutine 中串行触发。
type Handlers struct {
	OnReasoning func(string)
	OnContent   func(string)
	// OnToolCall 在流结束时对每个完整工具调用回调一次
	OnToolCall func(name, args, id string)
	OnUsage    func(Usage)
}

// ChatRequest 内部请求体。
type chatRequest struct {
	Model       string         `json:"model"`
	Messages    []Message      `json:"messages"`
	Stream      bool           `json:"stream"`
	Temperature float64        `json:"temperature,omitempty"`
	MaxTokens   int            `json:"max_tokens,omitempty"`
	Tools       []ToolDef      `json:"tools,omitempty"`
	ToolChoice  string         `json:"tool_choice,omitempty"`
	StreamOpt   *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role             string          `json:"role"`
			Content          string          `json:"content"`
			ReasoningContent string          `json:"reasoning_content"`
			Reasoning        string          `json:"reasoning"`
			ToolCalls        []toolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		Message struct {
			Content   string          `json:"content"`
			ToolCalls []toolCallDelta `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type toolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// APIError 表示服务端返回的错误。
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Status == 407 {
		msg := "代理要求认证（NTLM/Kerberos 集成认证暂不支持；请改用 Basic 代理、VPN/透明网关，或等待 SSPI 本机转发桥）"
		if e.Message != "" {
			msg += "：" + e.Message
		}
		return "HTTP 407: " + msg
	}
	if e.Message == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
}

// Endpoint 拼出 chat/completions 完整地址（兼容用户只填根域名的情形）。
func (c *Client) Endpoint() string {
	base := c.BaseURL
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	if !strings.HasSuffix(base, "/v1") && !strings.Contains(base, "/v1/") &&
		!strings.HasSuffix(base, "/v4") && !strings.Contains(base, "/api/") {
		// 常见错误：直接填了域名，补 /v1
		if !strings.Contains(strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://"), "/") {
			base += "/v1"
		}
	}
	return base + "/chat/completions"
}

// cleartextAPIKeyWarn 判断端点是否走明文且不在回环/私网内；是则返回警告文案。
func cleartextAPIKeyWarn(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	if !strings.EqualFold(u.Scheme, "http") {
		return ""
	}
	h := u.Hostname()
	if isLocalHostname(h) {
		return "" // localhost/127.0.0.1/::1/私网地址：局域网自建，明文可接受
	}
	return fmt.Sprintf("端点 %s 使用明文 http 且不在私网内：API Key 会被明文发出去，请改用 https 或局域网地址", u.Host)
}

// isLocalHostname 判断主机名是否明显是本机或私网。
func isLocalHostname(h string) bool {
	h = strings.ToLower(strings.Trim(h, "[]"))
	if h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return true
	}
	for _, p := range []string{"10.", "192.168.", "169.254.", "fd", "fc", "fe80"} {
		if strings.HasPrefix(h, p) {
			return true
		}
	}
	// 172.16.0.0/12：第二个字节 16~31
	if strings.HasPrefix(h, "172.") {
		parts := strings.Split(h, ".")
		if len(parts) >= 2 {
			if n, err := strconv.Atoi(parts[1]); err == nil && n >= 16 && n <= 31 {
				return true
			}
		}
	}
	return false
}

func newSessionID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "win7h-" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("win7h-%d", time.Now().UnixNano())
}

// setHeaders 统一请求头。OpenCode Go 要求稳定会话 ID；
// 其他 OpenAI 兼容端点会忽略它。
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "WIN7Harness/"+core.Version)
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.Org != "" {
		req.Header.Set("OpenAI-Organization", c.Org)
	}
	if core.IsOpenCodeGo(c.BaseURL) {
		sid := c.SessionID
		if sid == "" {
			sid = newSessionID()
		}
		req.Header.Set("x-opencode-session", sid)
	}
}

func (c *Client) newRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// http:// 且目标不是回环/私网：API Key 会以明文 Bearer 出网，
	// 不拒绝（局域网自建服务是合法场景），但必须让用户知道。
	if warn := cleartextAPIKeyWarn(c.Endpoint()); warn != "" {
		core.Logf("llm: %s", warn)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	c.setHeaders(req)
	return req, nil
}

// Chat 发起一次流式对话。stop 用于外部取消（用户点停止）。
func (c *Client) Chat(ctx context.Context, msgs []Message, tools []ToolDef,
	temp float64, maxTokens int, h Handlers) (Usage, error) {
	// 断线续写的细节在 resume.go：连接中途断开且已收到部分正文时自动接上。
	return c.ChatWithResume(ctx, msgs, tools, temp, maxTokens, h)
}

func (c *Client) do(ctx context.Context, body []byte) (*http.Response, error) {
	var resp *http.Response
	var err error
	// 建连阶段允许重试：限流、上游 5xx、网络抖动各退避再试，最多 3 次。
	for attempt := 0; ; attempt++ {
		resp, err = c.tryPost(ctx, body)
		if err == nil && !retryableStatus(resp.StatusCode) {
			break
		}
		if attempt >= c.maxRetries() {
			break
		}
		var wait time.Duration
		if err != nil {
			if !retryableNetErr(ctx, err) {
				break
			}
			wait = backoff(c.retryBase(), attempt)
			core.Logf("llm: 建连失败，第 %d 次重试，等待 %v：%v", attempt+1, wait, err)
		} else {
			wait = retryAfter(resp.Header)
			if wait <= 0 {
				wait = backoff(c.retryBase(), attempt)
			}
			core.Logf("llm: 上游返回 %d，第 %d 次重试，等待 %v", resp.StatusCode, attempt+1, wait)
			io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
			resp.Body.Close()
		}
		if !sleepCtx(ctx, wait) {
			if err != nil {
				return nil, friendlyNetErr(err)
			}
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, friendlyNetErr(err)
	}
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		resp.Body.Close()
		msg := extractErr(data)
		// 端点不支持 tools / max_tokens / stream_options 时自动降级重试一次
		if resp.StatusCode == 400 || resp.StatusCode == 422 {
			if trimmed, stripped, changed := degrade(body, msg); changed {
				core.Logf("llm: 上游 %d，已自动降级重试（剥离 %s 后重发一次）", resp.StatusCode, strings.Join(stripped, "、"))
				resp2, err2 := c.tryPost(ctx, trimmed)
				if err2 == nil && resp2.StatusCode < 400 {
					return resp2, nil
				}
				if resp2 != nil {
					d2, _ := io.ReadAll(io.LimitReader(resp2.Body, 8192))
					resp2.Body.Close()
					return nil, &APIError{Status: resp2.StatusCode, Message: extractErr(d2)}
				}
			}
		}
		return nil, &APIError{Status: resp.StatusCode, Message: msg}
	}
	return resp, nil
}

func (c *Client) tryPost(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := c.newRequest(ctx, body)
	if err != nil {
		return nil, err
	}
	return c.http.Do(req)
}

func extractErr(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if json.Unmarshal(data, &e) == nil {
		if e.Error.Message != "" {
			return e.Error.Message
		}
		if e.Message != "" {
			return e.Message
		}
		if e.Detail != "" {
			return e.Detail
		}
	}
	s := strings.TrimSpace(string(data))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// degrade 去掉可能导致 400 的高级参数。
func degrade(body []byte, errMsg string) ([]byte, []string, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return body, nil, false
	}
	low := strings.ToLower(errMsg)
	var stripped []string
	changed := false
	if strings.Contains(low, "tool") || strings.Contains(low, "function") {
		delete(m, "tools")
		delete(m, "tool_choice")
		stripped = append(stripped, "tools", "tool_choice")
		changed = true
	}
	if strings.Contains(low, "max_tokens") || strings.Contains(low, "max_completion") {
		delete(m, "max_tokens")
		stripped = append(stripped, "max_tokens")
		changed = true
	}
	if strings.Contains(low, "stream_options") {
		delete(m, "stream_options")
		stripped = append(stripped, "stream_options")
		changed = true
	}
	if strings.Contains(low, "temperature") {
		delete(m, "temperature")
		stripped = append(stripped, "temperature")
		changed = true
	}
	if !changed {
		// 未知原因：不再盲目降级。
		// 此前会把 tools 整个剥掉再发一次——模型失去工具能力、任务照样跑完、
		// 用户却以为模型"不肯用工具"，且这次重发额外计费。现在按原错误返回。
		return body, nil, false
	}
	out, err := json.Marshal(m)
	if err != nil {
		return body, nil, false
	}
	return out, stripped, true
}

// sanitize 清理非法消息（空 tool_calls 等），并按角色顺序保证 tool 消息有对应调用。
func sanitize(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "assistant" && m.Content == "" && len(m.ToolCalls) == 0 {
			continue
		}
		if m.Role == "tool" && m.ToolCallID == "" {
			continue
		}
		if m.Role == "assistant" {
			for i := range m.ToolCalls {
				if m.ToolCalls[i].Type == "" {
					m.ToolCalls[i].Type = "function"
				}
			}
		}
		out = append(out, m)
	}
	return out
}

const (
	maxContentBytes  = 8 << 20 // 单次回复正文上限 8MB
	maxToolArgsBytes = 1 << 20 // 单个工具调用参数上限 1MB
)

func (c *Client) readSSETraced(resp *http.Response, h Handlers) (Usage, string, bool, error) {
	var usage Usage
	var contentBuf strings.Builder
	var reasoningBuf strings.Builder

	type acc struct {
		id      string
		name    string
		args    strings.Builder
		emitted bool
	}
	calls := map[int]*acc{}
	var order []int

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	// 判断"这条流是正常说完的，还是半路被掐断的"：
	// 正常结束时服务端一定会给出 [DONE] 或 finish_reason，两者都没有就到 EOF 的，
	// 属于静默截断（代理超时、NAT 断链最常见），必须当错误处理才能触发续写。
	sawDone := false
	sawFinish := false
	malformed := 0

	// 空闲看门狗：90 秒没有一个字节进来就认为流死了（NAT 掉线/假死代理），
	// 直接关掉响应体，Scan() 会以错误返回，避免无限挂在 Read 上。
	var lastActivity int64
	atomicStore64(&lastActivity, time.Now().UnixNano())
	idleDone := make(chan struct{})
	core.SafeGo("llm/client.go:idle-watchdog", func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-idleDone:
				return
			case <-t.C:
				if time.Now().UnixNano()-atomicLoad64(&lastActivity) > int64(90*time.Second) {
					_ = resp.Body.Close()
					return
				}
			}
		}
	})
	defer close(idleDone)

	// SSE 规范（WHATWG §服务器发送事件）：一个事件块由多行组成，
	// 以空行分隔；同一事件里多个 data: 行必须用 \n 拼接后再解析。
	// 之前按"一行 data 即一个完整 JSON"处理，多行 data 会被拆成两半、
	// 两次解析失败、正文无声丢失。
	type event struct{ data []string }
	var cur event
	flush := func() (done bool, err error) {
		if len(cur.data) == 0 {
			return false, nil
		}
		payload := strings.Join(cur.data, "\n")
		cur.data = cur.data[:0]
		if payload == "[DONE]" {
			sawDone = true
			return true, nil
		}
		var ch chatChunk
		if err := json.Unmarshal([]byte(payload), &ch); err != nil {
			malformed++
			return false, nil
		}
		if ch.Error != nil && ch.Error.Message != "" {
			return false, &APIError{Status: 200, Message: ch.Error.Message}
		}
		if ch.Usage != nil {
			usage = *ch.Usage
		}
		for _, choice := range ch.Choices {
			if choice.FinishReason != "" {
				sawFinish = true
			}
			d := choice.Delta
			if d.ReasoningContent != "" {
				reasoningBuf.WriteString(d.ReasoningContent)
				if h.OnReasoning != nil {
					h.OnReasoning(d.ReasoningContent)
				}
			}
			if d.Reasoning != "" {
				reasoningBuf.WriteString(d.Reasoning)
				if h.OnReasoning != nil {
					h.OnReasoning(d.Reasoning)
				}
			}
			if d.Content != "" {
				if contentBuf.Len()+len(d.Content) > maxContentBytes {
					return false, &APIError{Status: 0, Message: "模型输出超过 8MB 上限，已中止（stream too large）"}
				}
				contentBuf.WriteString(d.Content)
				if h.OnContent != nil {
					h.OnContent(d.Content)
				}
			}
			for _, tc := range d.ToolCalls {
				a, ok := calls[tc.Index]
				if !ok {
					a = &acc{}
					calls[tc.Index] = a
					order = append(order, tc.Index)
				}
				if tc.ID != "" {
					a.id = tc.ID
				}
				if tc.Function.Name != "" {
					a.name = tc.Function.Name
				}
				if a.args.Len()+len(tc.Function.Arguments) > maxToolArgsBytes {
					return false, &APIError{Status: 0, Message: "工具调用参数超过 1MB 上限，已中止（stream too large）"}
				}
				a.args.WriteString(tc.Function.Arguments)
			}
		}
		return false, nil
	}

	for scanner.Scan() {
		atomicStore64(&lastActivity, time.Now().UnixNano())
		line := strings.TrimRight(scanner.Text(), "\r")
		switch {
		case line == "":
			// 空行 = 事件块结束
			done, err := flush()
			if err != nil {
				return usage, contentBuf.String(), len(order) > 0, err
			}
			if done {
				goto out
			}
			continue
		case strings.HasPrefix(line, ":"):
			// 注释行（心跳）
			continue
		case strings.HasPrefix(line, "event:"):
			// 事件名：本实现按 data 内的 type 字段区分，忽略 event 字段
			continue
		case strings.HasPrefix(line, "data:"):
			payload := strings.TrimPrefix(line, "data:")
			payload = strings.TrimPrefix(payload, " ")
			cur.data = append(cur.data, payload)
		default:
			// 未知字段（id:/retry: 等）：忽略
			continue
		}
	}
	// 流结束时不足一个完整事件块：全部 flush
	if _, err := flush(); err != nil {
		return usage, contentBuf.String(), len(order) > 0, err
	}

out:
	if err := scanner.Err(); err != nil {
		// 把已收到的正文一并带回：上层据此决定能不能续写
		return usage, contentBuf.String(), len(order) > 0, err
	}
	if malformed > 0 {
		core.Logf("llm: SSE 中有 %d 行解析失败（已跳过），正文完整性不保证", malformed)
	}
	if !sawDone && !sawFinish {
		return usage, contentBuf.String(), len(order) > 0, io.ErrUnexpectedEOF
	}
	if h.OnUsage != nil {
		h.OnUsage(usage)
	}
	if h.OnToolCall != nil {
		for i, idx := range order {
			a := calls[idx]
			id := a.id
			if id == "" {
				id = fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), i)
			}
			h.OnToolCall(a.name, a.args.String(), id)
		}
	}
	return usage, contentBuf.String(), len(order) > 0, nil
}

// atomicStore64 / atomicLoad64 封装 atomic 操作（llm 包避免直接依赖 sync/atomic 类型）。
func atomicStore64(p *int64, v int64) { atomic.StoreInt64(p, v) }
func atomicLoad64(p *int64) int64     { return atomic.LoadInt64(p) }

// readWhole 处理非流式（整包）响应。返回用量、正文与是否出现工具调用。
func (c *Client) readWhole(resp *http.Response, h Handlers) (Usage, string, bool, error) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return Usage{}, "", false, err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content          string          `json:"content"`
				ReasoningContent string          `json:"reasoning_content"`
				ToolCalls        []toolCallDelta `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage *Usage `json:"usage"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return Usage{}, "", false, fmt.Errorf("解析响应失败: %w", err)
	}
	var usage Usage
	if out.Usage != nil {
		usage = *out.Usage
	}
	if len(out.Choices) == 0 {
		return usage, "", false, errors.New("响应中没有 choices")
	}
	m := out.Choices[0].Message
	if m.ReasoningContent != "" && h.OnReasoning != nil {
		h.OnReasoning(m.ReasoningContent)
	}
	if m.Content != "" && h.OnContent != nil {
		h.OnContent(m.Content)
	}
	hasTool := len(m.ToolCalls) > 0
	if h.OnToolCall != nil {
		for _, tc := range m.ToolCalls {
			id := tc.ID
			if id == "" {
				id = fmt.Sprintf("call_%d", time.Now().UnixNano())
			}
			h.OnToolCall(tc.Function.Name, tc.Function.Arguments, id)
		}
	}
	if h.OnUsage != nil {
		h.OnUsage(usage)
	}
	return usage, m.Content, hasTool, nil
}

// Ping 测试连通性：优先 GET /models，回退空对话。
func (c *Client) Ping(ctx context.Context) (string, error) {
	base := c.Endpoint()
	base = strings.TrimSuffix(base, "/chat/completions")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return "", err
	}
	c.setHeaders(req)
	ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req = req.WithContext(ctx2)
	resp, err := c.http.Do(req)
	if err != nil {
		// 不发“hi”测试对话回退：/models 连不上多半是网络/证书/代理问题，
		// 发对话只会在用户毫无察觉的情况下真实计费一次。
		return "", fmt.Errorf("连通性探测失败（无法到达 /models）：%w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 400 {
		var out struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if json.Unmarshal(data, &out) == nil && len(out.Data) > 0 {
			return fmt.Sprintf("连接成功，可用模型 %d 个（如 %s）", len(out.Data), out.Data[0].ID), nil
		}
		return "连接成功", nil
	}
	// /models 走不通（404/401/5xx——部分 OpenAI 兼容网关对 /models 鉴权或路径处理与聊天接口不一致，
	// 实测有则 401 但 chat 渠道 200 的网关）：退回一次极短对话验证（max_tokens=1 ≈ 2 token），
	// 用 core.Logf 明示这笔最小验证，不静默烧钱也不误判“端点就废了”。
	modelsErr := fmt.Sprintf("HTTP %d", resp.StatusCode)
	ctx3, cancel2 := context.WithTimeout(ctx, 20*time.Second)
	defer cancel2()
	msgs := []Message{{Role: "user", Content: "hi"}}
	if _, err := c.Chat(ctx3, msgs, nil, 0, 16, Handlers{}); err != nil {
		return "", fmt.Errorf("连通性探测失败：/models 返回 %s，极短对话验证也失败：%w", modelsErr, err)
	}
	core.Logf("llm: /models 返回 %s，已退回一次极短对话验证连通性（max_tokens=1，约几个 token）", modelsErr)
	return "连接成功（/models 不可用，已通过极短对话验证）", nil
}

// ListModels 从 GET {base}/models 拉取模型 ID 列表。
// 只读元数据，不产生对话 token；端点不实现 /models 时返回错误，由界面提示用户手填。
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	base := strings.TrimSuffix(c.Endpoint(), "/chat/completions")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	ctx2, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req = req.WithContext(ctx2)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("拉取模型列表失败：%w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		snip := strings.TrimSpace(string(data))
		if len([]rune(snip)) > 200 {
			snip = string([]rune(snip)[:200]) + "…"
		}
		if snip == "" {
			return nil, fmt.Errorf("拉取模型列表失败：HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("拉取模型列表失败：HTTP %d：%s", resp.StatusCode, snip)
	}
	var out struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
		Models []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("模型列表不是合法 JSON：%w", err)
	}
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, m := range out.Data {
		add(m.ID)
	}
	for _, m := range out.Models {
		if m.ID != "" {
			add(m.ID)
		} else {
			add(m.Name)
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("端点返回成功，但没有可识别的模型 ID")
	}
	return ids, nil
}

// WithModel 返回绑定到另一个模型的客户端副本，用于同一次会话里按任务
// 切换主/快模型。副本共用底层连接池与 TLS 配置，不会额外建连；
// 传空串时原样返回，方便调用方不必判空。
func (c *Client) WithModel(model string) *Client {
	if c == nil || model == "" || model == c.Model {
		return c
	}
	cp := *c
	cp.Model = model
	return &cp
}
