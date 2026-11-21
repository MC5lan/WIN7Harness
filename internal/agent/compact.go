package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"win7harness/internal/llm"
	"win7harness/internal/tools"
)

// 上下文压缩：对标 Claude Code / Codex 的 auto-compact。
// 超出预算时把早期对话交给模型摘要成要点，而不是直接丢弃。

const (
	compactKeep  = 8    // 压缩时保留的最近消息条数
	compactMin   = 12   // 少于该条数不压缩
	compactModel = 8000 // 摘要允许的最大字符

	// compactRatio 触发压缩时允许占用的上下文比例。
	// 留一半给"接下来这一轮"：系统提示、工具结果、模型输出。
	// 压太早会丢实现细节（模型忘了自己改过哪个文件），压太晚会被网关直接拒。
	compactRatio = 0.5
	// defaultContextWindow 是不知道模型窗口时的兜底（主流模型 128k）。
	defaultContextWindow = 128000
)

// contextWindow 返回本引擎使用的上下文窗口（token）。
func (e *Engine) contextWindow() int {
	if e.ContextWindow > 0 {
		return e.ContextWindow
	}
	return defaultContextWindow
}

// contextTokenLimit 返回留给“系统提示 + 历史消息”的 token 预算。
// 只占窗口的一半左右，剩下的留给本轮工具结果、模型输出和估算误差。
func (e *Engine) contextTokenLimit() int {
	w := e.contextWindow()
	limit := int(float64(w) * compactRatio)
	if limit < 2000 {
		limit = 2000
	}
	// 32 位进程地址空间有限：压缩本身要把历史拼成一份大字符串，
	// 阈值放太大反而容易 OOM，给个上限。
	if strconv.IntSize == 32 && limit > 60000 {
		limit = 60000
	}
	return limit
}

func estimateChars(msgs []llm.Message) int {
	total := 0
	for i := range msgs {
		total += messageSize(&msgs[i])
	}
	return total
}

// messageSize 估算一条消息的字符成本；图片 data URL 按字符数计入——
// 此前完全不计 Parts，多图长会话会低估上下文几个数量级，
// 该压缩时压不动，或每轮带着几百 KB 请求体硬塞进 32 位地址空间。
func messageSize(m *llm.Message) int {
	// Reasoning 只保留在本地 UI，不会发回模型，也不计入上下文预算。
	l := len(m.Content) + 40
	for _, tc := range m.ToolCalls {
		l += len(tc.Function.Arguments) + 20
	}
	for _, p := range m.Parts {
		switch p.Type {
		case "image_url":
			if p.ImageURL != nil {
				l += len(p.ImageURL.URL)
			}
		default:
			l += len(p.Text)
		}
	}
	return l
}

// compactIfNeeded 在上下文接近预算时自动压缩，返回是否发生压缩。
func (e *Engine) compactIfNeeded(ctx context.Context) bool {
	e.mu.Lock()
	size := estimateMessagesTokens(e.Messages)
	n := len(e.Messages)
	e.mu.Unlock()
	if size < e.contextTokenLimit() || n < 2 {
		return false
	}
	// 消息条数少但单条极大（一次 grep/test 输出、超长文件）时，
	// 摘要压缩没有可切的历史；先把超大工具结果就地压成摘要。
	if n < compactMin {
		return e.shrinkLargeToolMessages(ctx)
	}
	if _, err := e.Compact(ctx); err == nil {
		// 摘要后仍可能因为最近几条超大工具结果超预算；就地压缩，避免下一轮再次触发。
		e.shrinkLargeToolMessages(ctx)
		return true
	}
	return false
}

// shrinkLargeToolMessages 在无法做历史摘要时，把单条超大工具结果压回预算内。
// 不删除消息、不破坏 tool 配对，只替换内容；全文可由 artifact/重新读取找回。
func (e *Engine) shrinkLargeToolMessages(ctx context.Context) bool {
	const toolTokBudget = 2500
	e.mu.Lock()
	changed := 0
	for i := range e.Messages {
		m := &e.Messages[i]
		if m.Role != "tool" || estimateTokens(m.Content) <= toolTokBudget {
			continue
		}
		// 按字符预算调用现有摘要器（约 3 字符/token 的保守换算）。
		m.Content = tools.SummarizeToolOutput(m.Content, toolTokBudget*3)
		changed++
	}
	e.mu.Unlock()
	if changed > 0 {
		e.emit(Event{Kind: "notice", Text: fmt.Sprintf("上下文里 %d 条超长工具结果已就地压缩（原文仍可通过重新读取/日志查看）。", changed)})
		return true
	}
	return false
}

// Compact 把早期对话压缩成摘要，保留最近若干条原文。供自动压缩与 /compact 命令共用。
func (e *Engine) Compact(ctx context.Context) (string, error) {
	e.mu.Lock()
	hist := make([]llm.Message, len(e.Messages))
	copy(hist, e.Messages)
	e.mu.Unlock()

	if len(hist) < compactMin {
		return "当前对话较短，无需压缩", nil
	}

	split := len(hist) - compactKeep
	// 严格对齐：不能把 tool 结果与它的 assistant 调用切开。
	// assistant(tool_calls) 与它的 tool 响应自成一段，不需要额外回退——
	// 此前的 assistant 回退会把 split 往回挪到 tool 消息上，造出孤儿。
	for split > 0 && hist[split].Role == "tool" {
		split--
	}
	if split <= 0 {
		return "当前对话较短，无需压缩", nil
	}

	old := hist[:split]
	var transcript strings.Builder
	for _, m := range old {
		switch m.Role {
		case "user":
			transcript.WriteString("用户：" + clip(m.Content, 600) + "\n")
		case "assistant":
			if m.Content != "" {
				transcript.WriteString("助手：" + clip(m.Content, 600) + "\n")
			}
			for _, tc := range m.ToolCalls {
				transcript.WriteString("[调用工具 " + tc.Function.Name + " " + clip(tc.Function.Arguments, 200) + "]\n")
			}
		case "tool":
			transcript.WriteString("[" + m.Name + " 结果：" + clip(m.Content, 200) + "]\n")
		}
	}

	e.emit(Event{Kind: "status", Text: "正在压缩上下文…"})
	prompt := []llm.Message{
		{Role: "system", Content: "你是对话压缩器。把给定的历史对话压缩成结构化要点，供后续继续工作使用。" +
			"只输出要点本身，不要寒暄。必须保留：用户目标与约束、已确认的关键事实（文件路径、命令、版本、结论）、" +
			"已完成的改动、尚未完成的待办、失败过的尝试与其原因。丢弃寒暄与重复内容。中文输出，不超过 600 字。"},
		{Role: "user", Content: "请压缩以下历史对话：\n\n" + clip(transcript.String(), compactTranscriptChars(e.contextWindow()))},
	}

	var sb strings.Builder
	// 压缩是"读得多、想得少"的活，开启路由时交给快速模型，省下的钱是纯赚
	client, usedModel := e.ClientFor(JobCompact)
	usage, err := client.Chat(ctx, prompt, nil, 0.2, 2000, llm.Handlers{
		OnContent: func(s string) { sb.WriteString(s) },
	})
	e.addUsageFor(usedModel, usage)
	sum := strings.TrimSpace(sb.String())
	if err != nil || sum == "" {
		// 摘要失败时退化为规则摘要，不能因为压缩把对话搞丢
		sum = CompactSummary(old)
		if sum == "" {
			if err != nil {
				return "", err
			}
			return "上下文压缩失败，已保留原文", nil
		}
	}
	// 按字符截断，不能按字节：中文一个字符占 3 字节，按字节切会把最后一个字劈成乱码。
	sum = clip(sum, compactModel)

	kept := make([]llm.Message, 0, len(hist)-split+2)
	kept = append(kept, llm.Message{
		Role:    "user",
		Content: "【前面 " + fmt.Sprint(len(old)) + " 条对话已压缩为以下摘要，作为背景继续】\n" + sum,
	})
	kept = append(kept, llm.Message{
		Role:    "assistant",
		Content: "已了解上述背景与进度，继续完成任务。",
	})
	kept = append(kept, hist[split:]...)

	e.mu.Lock()
	e.Messages = kept
	e.mu.Unlock()
	e.verifyMu.Lock()
	e.compactCount++
	e.verifyMu.Unlock()

	return fmt.Sprintf("已压缩 %d 条早期对话（%d 字符 → 剩余 %d 条）", len(old), estimateChars(old), len(kept)), nil
}

// compactTranscriptChars 是送给摘要模型的转录字符上限。
// 取窗口的 3 倍字符但不超过 20 万，避免 32 位进程一次拼出超大字符串。
func compactTranscriptChars(window int) int {
	n := window * 3
	if n < 40000 {
		n = 40000
	}
	if n > 200000 {
		n = 200000
	}
	return n
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// Stats 返回上下文消息条数与估算字符数，供 /status 展示。
func (e *Engine) Stats() (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.Messages), estimateChars(e.Messages)
}

// ContextInfo 返回上下文占用（条数/字符/token）、窗口与预算，供 /context 与界面展示。
// 结果缓存 500ms：侧栏每帧都会调用，不能每帧遍历全部历史做 token 估算。
func (e *Engine) ContextInfo() (msgs, chars, histTokens, window, limit int) {
	e.ctxMu.Lock()
	if !e.ctxAt.IsZero() && time.Since(e.ctxAt) < 500*time.Millisecond {
		msgs, chars, histTokens = e.ctxMsgs, e.ctxChars, e.ctxTokens
		e.ctxMu.Unlock()
		window = e.contextWindow()
		limit = e.contextTokenLimit()
		return
	}
	e.ctxMu.Unlock()

	e.mu.Lock()
	msgs = len(e.Messages)
	chars = estimateChars(e.Messages)
	histTokens = estimateMessagesTokens(e.Messages)
	e.mu.Unlock()

	e.ctxMu.Lock()
	e.ctxAt, e.ctxMsgs, e.ctxChars, e.ctxTokens = time.Now(), msgs, chars, histTokens
	e.ctxMu.Unlock()
	window = e.contextWindow()
	limit = e.contextTokenLimit()
	return
}

// ToolDefsStats 返回本轮工具定义数量与估算字符数。
func (e *Engine) ToolDefsStats() (n, chars int) {
	defs := e.toolDefsForTurn()
	for _, d := range defs {
		n++
		chars += len(d.Function.Name) + len(d.Function.Description) + len(d.Function.Parameters)
	}
	return
}

// ToolDefsTokens 返回本轮工具定义的估算 token 数（计入上下文预算）。
func (e *Engine) ToolDefsTokens() int {
	defs := e.toolDefsForTurn()
	total := 0
	for _, d := range defs {
		total += estimateTokens(d.Function.Name) + estimateTokens(d.Function.Description) + estimateTokens(string(d.Function.Parameters))
	}
	return total
}

// CompactCount 返回本次会话的累计压缩次数。
func (e *Engine) CompactCount() int {
	e.verifyMu.Lock()
	defer e.verifyMu.Unlock()
	return e.compactCount
}
