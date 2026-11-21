package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// 流式断线续传（应用层续写）。
//
// 现实情况：OpenAI 兼容的 SSE 协议里没有"从第 N 个 token 继续"的标准做法
// （Last-Event-ID 各家基本不实现），所以真正能用的手段是：
// 连接断了但已经收到一段正文时，把这段正文作为 assistant 消息补进上下文，
// 再让模型"接着往下写、不要重复"，把两段输出拼起来。
//
// 边界：
//   - 一个都没收到（连不上、首包就断）→ 交给上层原有重试逻辑，这里不掺和；
//   - 已经收到工具调用片段 → 不续写（参数被截断时拼出来的工具调用是错的，
//     宁可报错让上层重来）；
//   - 用户主动停止（ctx 取消）→ 绝不续写。

// resumeNotice 续写时追加的指令。措辞要明确"不要重复"，否则模型会把整段重写一遍。
const resumeNotice = "（说明：上一条回复在网络中断处被截断了，请紧接着断点继续写完，" +
	"不要重复已经输出的内容，不要重新开头，也不要道歉或解释。）"

// errNoResume 表示本次失败不适合续写。
var errNoResume = errors.New("no resume")

// errReasoningOnly 表示模型只输出了 reasoning_content，没有正文或工具调用。
// 这类响应重试通常会再次把输出预算烧在思考上，不能按“空响应”盲目重试。
var errReasoningOnly = errors.New("模型只返回了思考过程（reasoning_content），没有生成正文或工具调用。" +
	"通常是最大输出 token 被推理占满，或该模型/网关要求关闭思考；" +
	"请调大“最大输出 token”、缩短上下文、换模型，或在该端点支持的参数里关闭 thinking 后重试")

// ChatWithResume 在 Chat 的基础上加一次断线续写。
// 返回的 usage 为两次请求的累加。
func (c *Client) ChatWithResume(ctx context.Context, msgs []Message, tools []ToolDef,
	temp float64, maxTokens int, h Handlers) (Usage, error) {

	usage, content, reasoning, hasTool, err := c.chatOnceWithReasoning(ctx, msgs, tools, temp, maxTokens, h)
	if err == nil {
		if strings.TrimSpace(content) != "" || hasTool {
			return usage, nil
		}
		if strings.TrimSpace(reasoning) != "" {
			// 有思考、无正文：不要重试，否则会再次消耗一整轮推理预算。
			return usage, errReasoningOnly
		}
		// 真空响应：连接正常但模型一个字都没吐（网关抽风、上下文被裁剪到只剩空壳等）。
		// 直接重试一次比把空回复返回给用户强，但要限次，别把 40 秒的超时重成 4 分钟。
		u2, c2, r2, ht2, err2 := c.chatOnceWithReasoning(ctx, msgs, tools, temp, maxTokens, h)
		usage.PromptTokens += u2.PromptTokens
		usage.CompletionTokens += u2.CompletionTokens
		usage.TotalTokens += u2.TotalTokens
		if err2 != nil {
			return usage, err2
		}
		if strings.TrimSpace(c2) == "" && !ht2 {
			if strings.TrimSpace(r2) != "" {
				return usage, errReasoningOnly
			}
			return usage, errors.New("模型连续两次返回空响应（无正文也无工具调用），请检查模型名、上下文长度或稍后重试")
		}
		return usage, nil
	}
	if !canResume(ctx, err, content) {
		return usage, err
	}

	// 拼接续写上下文：原消息 + 已输出的部分 + 续写指令。
	// 注意 msgs 是上层的切片，必须复制，别污染调用方的历史。
	resumeMsgs := make([]Message, 0, len(msgs)+2)
	resumeMsgs = append(resumeMsgs, msgs...)
	resumeMsgs = append(resumeMsgs,
		Message{Role: "assistant", Content: content},
		Message{Role: "user", Content: resumeNotice},
	)

	u2, _, _, _, err2 := c.chatOnceWithReasoning(ctx, resumeMsgs, tools, temp, maxTokens, h)
	usage.PromptTokens += u2.PromptTokens
	usage.CompletionTokens += u2.CompletionTokens
	usage.TotalTokens += u2.TotalTokens
	if err2 != nil {
		return usage, fmt.Errorf("网络中断后已自动续写仍失败：%v", err2)
	}
	return usage, nil
}

// chatOnceWithReasoning 包一层 chatOnce，收集 reasoning_content 但保留原回调。
// 上层需要区分“真空响应”和“只思考没正文”，否则会把推理预算重复烧掉。
func (c *Client) chatOnceWithReasoning(ctx context.Context, msgs []Message, tools []ToolDef,
	temp float64, maxTokens int, h Handlers) (Usage, string, string, bool, error) {
	var rb strings.Builder
	h2 := h
	orig := h.OnReasoning
	h2.OnReasoning = func(s string) {
		rb.WriteString(s)
		if orig != nil {
			orig(s)
		}
	}
	u, content, hasTool, err := c.chatOnce(ctx, msgs, tools, temp, maxTokens, h2)
	return u, content, rb.String(), hasTool, err
}

// canResume 判断这次失败是否值得续写。
func canResume(ctx context.Context, err error, received string) bool {
	if ctx.Err() != nil {
		return false // 用户停止或整体超时
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false // 取消类错误：重试只会白等一轮
	}
	if strings.TrimSpace(received) == "" {
		return false
	}
	if errors.Is(err, errPartialToolCall) {
		return false
	}
	// 服务端明确报的协议/鉴权错误不算网络中断，重试无意义。
	// （SSE 里 error 事件被包装成 APIError{Status:200} 的场景也在此挡掉：
	//  那是服务端主动拒绝，续写只会再扣一次费且大概率再撞同一堵墙。）
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.Status == 200 {
			return false
		}
		if apiErr.Status >= 400 && apiErr.Status < 500 && apiErr.Status != 408 && apiErr.Status != 429 {
			return false
		}
	}
	return true
}

// chatOnce 发起一次流式请求，返回本次累计用量、已输出的正文、是否出现工具调用与错误。
func (c *Client) chatOnce(ctx context.Context, msgs []Message, tools []ToolDef,
	temp float64, maxTokens int, h Handlers) (Usage, string, bool, error) {

	if len(msgs) == 0 {
		return Usage{}, "", false, errors.New("消息为空")
	}
	wireMsgs := sanitize(msgs)
	c.markPromptCache(wireMsgs)
	req := chatRequest{
		Model:       c.Model,
		Messages:    wireMsgs,
		Stream:      true,
		Temperature: temp,
		MaxTokens:   maxTokens,
	}
	if len(tools) > 0 {
		req.Tools = tools
		req.ToolChoice = "auto"
	}
	req.StreamOpt = &streamOptions{IncludeUsage: true}

	body, err := json.Marshal(req)
	if err != nil {
		return Usage{}, "", false, err
	}

	// 每次尝试单独计时：续写不该被前半段的耗时吃光预算。
	reqCtx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	resp, err := c.do(reqCtx, body)
	if err != nil {
		return Usage{}, "", false, err
	}
	defer resp.Body.Close()

	ctype := resp.Header.Get("Content-Type")
	if ctype == "" {
		// 中间盒没给 Content-Type：嗅探首字节——'{' 按整包 JSON，否则按 SSE 流，
		// 不再把整个 JSON 响应当成 0 字节的 SSE 流报成 unexpected EOF。
		peek := make([]byte, 1)
		if n, err := io.ReadAtLeast(resp.Body, peek, 1); err == nil && n == 1 {
			body := io.NopCloser(io.MultiReader(bytes.NewReader(peek), resp.Body))
			resp.Body = body
			if peek[0] == '{' {
				ctype = "application/json"
			} else {
				ctype = "text/event-stream"
			}
		}
	}
	if strings.Contains(ctype, "application/json") && !strings.Contains(ctype, "event-stream") {
		// 非流式整包：正文与工具调用都回填，调用方能识别"不是空响应"，
		// 不会把已吐出的正文再请求一次（重复扣费+重复印出）。
		u, content, hasTool, err := c.readWhole(resp, h)
		return u, content, hasTool, err
	}
	u, content, hasTool, serr := c.readSSETraced(resp, h)
	if serr != nil && hasTool {
		// 工具参数被截断时拼出来的调用是错的，不允许续写
		serr = errPartialToolCall
	}
	return u, content, hasTool, serr
}

// errPartialToolCall 流在工具调用参数中途断开。
var errPartialToolCall = errors.New("工具调用参数未收完，连接已断开")
