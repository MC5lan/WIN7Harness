package core

import "fmt"

// 提示缓存与成本看板的计价基础。
//
// v1.32 起不再内置任何模型价目表：
//   - 应用不假设用户用的是哪家模型；
//   - 默认价只用于“量级正确”的估算，并在界面明确标注“按默认价”；
//   - 用户可在设置 → 预算里填输入/输出单价（美元/百万 token），
//     获得与自己的账单口径一致的成本看板。
//
// Price 是每百万 token 的美元单价。
type Price struct {
	In     float64 // 输入（未命中缓存）
	Out    float64 // 输出
	Cached float64 // 命中缓存的输入（通常为 In 的 10%~50%）
}

// DefaultPrice 是查不到用户单价时使用的保守估价（贵一档，避免把成本低估到零）。
var DefaultPrice = Price{In: 1.00, Out: 3.00, Cached: 0.30}

// LookupPrice 保留函数签名给成本看板使用；不再按模型名查表。
// 第二个返回值表示是否有“精确单价”——只有用户在设置里填了单价才算精确，
// 那条路径由 agent.Snapshot 直接读取配置，不在这里猜。
func LookupPrice(model string) (Price, bool) {
	return DefaultPrice, false
}

// Cost 按用量估算费用（美元）。
// cached 是命中缓存的输入 token（已包含在 prompt 中），按缓存单价计；
// 未命中部分按普通输入单价计。
func Cost(model string, prompt, cached, completion int) float64 {
	p := DefaultPrice
	if cached < 0 {
		cached = 0
	}
	if cached > prompt {
		cached = prompt
	}
	miss := prompt - cached
	const m = 1000000.0
	return (float64(miss)*p.In + float64(cached)*p.Cached + float64(completion)*p.Out) / m
}

// CostNoCache 是不考虑缓存、按全部输入计费的对照值，用来算"缓存省了多少"。
func CostNoCache(model string, prompt, completion int) float64 {
	return Cost(model, prompt, 0, completion)
}

// Money 把美元金额格式化成好读的字符串：小额给到 4 位小数，大额 2 位。
func Money(usd float64) string {
	switch {
	case usd <= 0:
		return "$0"
	case usd < 0.01:
		return fmt.Sprintf("$%.4f", usd)
	case usd < 1:
		return fmt.Sprintf("$%.3f", usd)
	default:
		return fmt.Sprintf("$%.2f", usd)
	}
}
