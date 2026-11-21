package agent

import "win7harness/internal/llm"

// Token 估算：不同脚本的字符/token 比例差异很大。
// 统一按 2.5 字符/token 会把大量中文低估（中文常常 1 字符≈1 token），
// 也会把英文高估；这里按 CJK / 非 CJK 分开估，宁可略保守。
const (
	tokensPerCJKChar   = 1.25
	tokensPerASCIIChar = 3.6
)

func isCJK(r rune) bool {
	switch {
	case r >= 0x2E80 && r <= 0x9FFF: // CJK 部首、标点、汉字
		return true
	case r >= 0xF900 && r <= 0xFAFF:
		return true
	case r >= 0x3040 && r <= 0x30FF: // 日文假名
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // 韩文
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // 全角字符
		return true
	default:
		return false
	}
}

// estimateTokens 估算一段文本的 token 数。
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	cjk, other := 0, 0
	for _, r := range s {
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	n := int(float64(cjk)/tokensPerCJKChar + float64(other)/tokensPerASCIIChar)
	if n < 1 {
		n = 1
	}
	return n
}

// estimateMessageTokens 估算一条消息的 token 成本（含工具参数与多模态文本）。
func estimateMessageTokens(m *llm.Message) int {
	// Reasoning 不进入后续请求（见 llm.Message.MarshalJSON），也不计预算。
	n := estimateTokens(m.Content) + 8
	for _, tc := range m.ToolCalls {
		n += estimateTokens(tc.Function.Arguments) + 6
	}
	for _, p := range m.Parts {
		switch p.Type {
		case "image_url":
			// 图片按固定视觉 token 粗估；具体值因模型而异，这里只用于预算。
			n += 800
		default:
			n += estimateTokens(p.Text)
		}
	}
	return n
}

// estimateMessagesTokens 估算一组消息的 token 成本。
func estimateMessagesTokens(msgs []llm.Message) int {
	total := 0
	for i := range msgs {
		total += estimateMessageTokens(&msgs[i])
	}
	return total
}
