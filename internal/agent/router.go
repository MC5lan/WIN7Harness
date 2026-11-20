package agent

// 模型路由：一次任务里真正需要"聪明"的只有主对话那几轮。
// 上下文压缩、只读子代理（读文件、跑搜索）属于读得多想得少的活，
// 用一个便宜模型跑同样的提示，质量够用、账单小一截。
//
// 刻意保持保守：只有显式打开路由且配置了快速模型才分流，
// 出问题时 CloseRouter 一下就能回到"全用主模型"的老行为。

import "win7harness/internal/llm"

// 任务类型：决定走哪个模型。
const (
	JobMain        = "main"      // 主对话
	JobCompact     = "compact"   // 上下文压缩
	JobSubReadOnly = "sub-read"  // 只读子代理（检索、读文件、查代码）
	JobSubWrite    = "sub-write" // 会写文件的子代理，保持主模型
)

// routerConfig 只读取路由配置（加锁，避免 UI 写、引擎读的 race）。
func (e *Engine) routerConfig() (on bool, fast, main string) {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	return e.RouterOn, e.FastModel, e.Model
}

// ModelFor 返回某类任务应使用的模型。路由关闭或未配置快速模型时一律回落主模型。
func (e *Engine) ModelFor(job string) string {
	on, fast, main := e.routerConfig()
	if !on || fast == "" || fast == main {
		return main
	}
	switch job {
	case JobCompact, JobSubReadOnly:
		return fast
	default:
		return main
	}
}

// ClientFor 返回绑定到目标模型的客户端副本，第二个返回值是实际选中的模型名。
// 客户端本身无状态（除连接池），副本共用同一连接池，不会额外建连。
func (e *Engine) ClientFor(job string) (*llm.Client, string) {
	m := e.ModelFor(job)
	return e.Client.WithModel(m), m
}

// RouterSummary 返回一行路由状态，供 /model 与设置面板显示。
func (e *Engine) RouterSummary() string {
	on, fast, main := e.routerConfig()
	if !on {
		return "模型路由：关闭（全部使用主模型 " + orDash(main) + "）"
	}
	if fast == "" {
		return "模型路由：已开启但未指定快速模型，实际仍全部使用 " + orDash(main)
	}
	return "模型路由：开启｜主对话 " + orDash(main) + "｜压缩与只读子代理 " + orDash(fast)
}

// SetRouter 打开/关闭路由并设定快速模型，返回生效后的摘要。
func (e *Engine) SetRouter(on bool, fast string) string {
	e.usageMu.Lock()
	e.RouterOn = on
	e.FastModel = fast
	e.usageMu.Unlock()
	return e.RouterSummary()
}

// Turns 返回累计调用轮数（不含子代理内部轮次）。
func (e *Engine) Turns() int {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	return e.turns
}

// fastUsage 返回快速模型桶的用量与轮数。
func (e *Engine) fastUsage() (int, int, int) {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	return e.fastTokIn, e.fastTokOut, e.fastTurns
}

// SubUsage 返回子代理累计用量与轮数，供成本看板单列。
func (e *Engine) SubUsage() (int, int, int) {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	return e.subTokIn, e.subTokOut, e.subTurns
}

// addUsageFor 把一次调用记到对应模型的账上：主模型与快速模型分开累计，
// 否则用两个不同单价的模型算总账必然算出个假数。
func (e *Engine) addUsageFor(model string, u llm.Usage) {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	if model != "" && e.FastModel != "" && model == e.FastModel && model != e.Model {
		e.fastTokIn += u.PromptTokens
		e.fastTokOut += u.CompletionTokens
		e.fastTurns++
		return
	}
	e.tokIn += u.PromptTokens
	e.tokOut += u.CompletionTokens
	e.tokCached += u.CachedTokens
	e.tokCacheW += u.CacheWriteTokens
	e.turns++
}
