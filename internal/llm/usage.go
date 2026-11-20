package llm

import (
	"encoding/json"
	"strings"
)

// 提示缓存的用量归一。
//
// 现实情况：同一个 OpenAI 兼容协议下，各家报缓存命中用的字段名完全不同，
// 而且都在自己的扩展里：
//
//	OpenAI       usage.prompt_tokens_details.cached_tokens
//	DeepSeek     usage.prompt_cache_hit_tokens / prompt_cache_miss_tokens
//	Anthropic    usage.cache_read_input_tokens / cache_creation_input_tokens
//	Moonshot 等  usage.cached_tokens（顶层直接给）
//
// 如果只认一种，换端点后命中率永远显示 0，用户会以为"缓存没生效"。
// 这里统一在反序列化阶段归一到 CachedTokens / CacheWriteTokens，
// 上层（成本看板、/stats）就不用关心各家差异了。

// usageAlias 用于避免递归调用 UnmarshalJSON。
type usageAlias Usage

// usageRaw 是各家字段的超集，缺哪个就是零值。
// 注意：不设顶层 CachedTokens 影子字段——encoding/json 的“浅者胜”规则会让别人被吞掉，
// 且字段语义混乱；缓存命中统一走 usageAlias.CachedTokens。
type usageRaw struct {
	usageAlias
	// DeepSeek
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	// Anthropic 兼容网关
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	// OpenAI
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// UnmarshalJSON 兼容各家缓存字段，并补齐缺失的 total_tokens。
func (u *Usage) UnmarshalJSON(data []byte) error {
	var raw usageRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*u = Usage(raw.usageAlias)

	// details 是可选对象，缺省时是 nil 指针，直接取值会 panic。
	detailsCached := 0
	if raw.PromptTokensDetails != nil {
		detailsCached = raw.PromptTokensDetails.CachedTokens
	}

	// 命中量取"第一个非零"，避免同一个响应里既有顶层 cached_tokens
	// 又有 details.cached_tokens 时被相加成两倍。
	for _, v := range []int{
		detailsCached,
		raw.PromptCacheHitTokens,
		raw.CacheReadInputTokens,
		raw.usageAlias.CachedTokens,
	} {
		if v > 0 {
			u.CachedTokens = v
			break
		}
	}
	if raw.CacheCreationInputTokens > 0 {
		u.CacheWriteTokens = raw.CacheCreationInputTokens
	}

	// 部分网关只给 prompt/completion，不给 total；缺了会让预算护栏算空。
	if u.TotalTokens <= 0 {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	// 缓存命中的部分已经包含在 prompt_tokens 里（各家口径一致），
	// 但个别代理会把 prompt_tokens 报成"未命中量"——此时命中量 > 未命中量很常见。
	// 正确归一是【累加】：总输入 = 命中 + 未命中。
	// 此前是直接覆盖（u.PromptTokens = u.CachedTokens），
	// 等于把未命中部分凭空抹掉，成本看板系统性低估、预算护栏失真。
	if u.CachedTokens > u.PromptTokens {
		u.PromptTokens += u.CachedTokens
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	return nil
}

// CachedRatio 返回输入 token 中被缓存命中的比例（0~1）。
// 没有输入 token 时返回 0，避免除零。
func (u Usage) CachedRatio() float64 {
	if u.PromptTokens <= 0 || u.CachedTokens <= 0 {
		return 0
	}
	r := float64(u.CachedTokens) / float64(u.PromptTokens)
	if r > 1 {
		r = 1
	}
	return r
}

// StablePrefix 判断两条消息是否构成相同前缀，用于本地估算"下一次请求能命中多少"。
// 提示缓存是按前缀匹配的，只要开头一段字节完全一致，那一整段就能命中；
// 因此把易变内容（时间戳、随机 ID）放进系统提示是最常见的失效原因。
func StablePrefix(a, b []Message) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content ||
			a[i].ToolCallID != b[i].ToolCallID || a[i].Name != b[i].Name {
			return i
		}
	}
	return n
}

// HasVolatilePrefix 检查系统提示里是否混入了每轮都会变的内容。
// 命中时上层会提示用户（或自动清理），否则缓存命中率会一直是 0。
func HasVolatilePrefix(system string) (string, bool) {
	// 只做低误报的检测：整点时间戳、日期时间串。
	for _, pat := range []string{"当前时间", "Current time", "现在时间"} {
		if strings.Contains(system, pat) {
			return pat, true
		}
	}
	return "", false
}
