package agent

import (
	"fmt"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/llm"
)

// 成本看板。
//
// 用户问"这版对话花了多少钱"时，答案必须是能算出来的数字，而不是"大概吧"。
// 这里把 token 用量、提示缓存命中率、按价目表估算的费用合到一处，
// 并同时给出"如果没缓存要花多少"，让缓存的价值可见。

// CostSnapshot 是一次成本快照。
type CostSnapshot struct {
	Model     string
	Turns     int
	TokIn     int
	TokOut    int
	Cached    int
	CacheW    int
	HitRate   float64 // 输入 token 中命中缓存的比例（累计）
	Cost      float64 // 实际估算费用（含缓存折扣）
	CostPlain float64 // 假设全未命中的估算费用
	Precise   bool    // 模型是否精确命中价目表
	LastHit   float64 // 最近一次请求的命中率，反映"当前上下文是否还稳定"

	// 以下为路由与子代理分项：主对话贵、辅助便宜，混在一行里看不出省钱在哪。
	RouterOn    bool
	FastModel   string
	FastTurns   int
	FastTokIn   int
	FastTokOut  int
	FastCost    float64
	SubTurns    int
	SubTokIn    int
	SubTokOut   int
	SubCost     float64
	SavedByFast float64 // 这些请求若走主模型要多花的钱
}

// recordCache 记录最近一次请求的缓存命中率。
func (e *Engine) recordCache(u llm.Usage) {
	r := u.CachedRatio()
	e.verifyMu.Lock()
	e.lastHitRate = r
	e.verifyMu.Unlock()
}

// price 返回当前计价口径：用户填了自定义单价就用它（缓存按输入价的 10% 估算），
// 否则用保守默认价。内置价目表已在 v1.32 移除，避免“模型名换了一版账单就不准”。
func (e *Engine) price() (core.Price, bool) {
	if e.PriceIn > 0 || e.PriceOut > 0 {
		in, out := e.PriceIn, e.PriceOut
		if in <= 0 {
			in = core.DefaultPrice.In
		}
		if out <= 0 {
			out = core.DefaultPrice.Out
		}
		return core.Price{In: in, Out: out, Cached: in * 0.1}, true
	}
	return core.DefaultPrice, false
}

func (e *Engine) costOf(prompt, cached, completion int) float64 {
	p, _ := e.price()
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

func (e *Engine) costNoCache(prompt, completion int) float64 {
	return e.costOf(prompt, 0, completion)
}

// CostSnapshot 汇总当前引擎的成本与缓存数据。
func (e *Engine) CostSnapshot() CostSnapshot {
	e.usageMu.Lock()
	in, out, cached, cacheW, turns := e.tokIn, e.tokOut, e.tokCached, e.tokCacheW, e.turns
	fIn, fOut, fTurns := e.fastTokIn, e.fastTokOut, e.fastTurns
	sIn, sOut, sTurns := e.subTokIn, e.subTokOut, e.subTurns
	sfi, sfo := e.subFastTokIn, e.subFastTokOut
	routerOn, fastModel := e.RouterOn, e.FastModel
	e.usageMu.Unlock()

	fastCost := 0.0
	savedByFast := 0.0
	if fTurns > 0 && fastModel != "" {
		fastCost = e.costOf(fIn, 0, fOut)
		// 自定义单价是全局口径；没有分模型价目表后，轻任务与主模型同价，
		// “走主模型要多花”只能是 0。保留字段是为了界面兼容。
	}
	// 子代理分价：快速路由（只读子代理）走 FastModel 价，其余走主模型价。
	// 此前直接按主模型价对整笔子代理用量计费，RouterOn 时子代理行会虚高。
	sMainIn, sMainOut := sIn-sfi, sOut-sfo
	if sMainIn < 0 {
		sMainIn = 0
	}
	if sMainOut < 0 {
		sMainOut = 0
	}
	subCost := e.costOf(sMainIn, 0, sMainOut)
	if fastModel != "" && (sfi+sfo) > 0 {
		subCost += e.costOf(sfi, 0, sfo)
	}
	_, precise := e.price()
	s := CostSnapshot{
		Model:     e.Model,
		Turns:     turns,
		TokIn:     in,
		TokOut:    out,
		Cached:    cached,
		CacheW:    cacheW,
		Cost:      e.costOf(in, cached, out),
		CostPlain: e.costNoCache(in, out),
		Precise:   precise,

		RouterOn:    routerOn,
		FastModel:   fastModel,
		FastTurns:   fTurns,
		FastTokIn:   fIn,
		FastTokOut:  fOut,
		FastCost:    fastCost,
		SubTurns:    sTurns,
		SubTokIn:    sIn,
		SubTokOut:   sOut,
		SubCost:     subCost,
		SavedByFast: savedByFast,
	}
	if in > 0 {
		s.HitRate = float64(cached) / float64(in)
		if s.HitRate > 1 {
			s.HitRate = 1
		}
	}
	e.verifyMu.Lock()
	s.LastHit = e.lastHitRate
	e.verifyMu.Unlock()
	return s
}

// CacheLine 返回一行缓存与成本摘要，供 /stats 与状态栏复用。
func (e *Engine) CacheLine() string {
	s := e.CostSnapshot()
	if s.Turns == 0 {
		return "还没有可统计的调用"
	}
	saved := s.CostPlain - s.Cost
	note := ""
	if !s.Precise {
		note = "（按默认价估算）"
	}
	line := fmt.Sprintf("缓存命中 %s（最近一轮 %s）｜累计 %s，未缓存需 %s，省下 %s%s",
		pct(s.HitRate), pct(s.LastHit), core.Money(s.Cost), core.Money(s.CostPlain), core.Money(saved), note)
	if s.Cached == 0 {
		// 命中率为 0 时给出可操作的解释，而不是让用户看着 0 发呆。
		line += "\n  提示：命中率为 0 通常说明系统提示每轮都在变（时间戳/随机信息），或端点不支持提示缓存。"
	}
	return line
}

// fastPart 拼出快速模型名，缺省时给一句人话而不是空着。
func fastPart(fast string) string {
	if fast == "" {
		return "（未指定快速模型）"
	}
	return "｜轻任务 " + fast
}

// CostBoard 返回完整的成本看板文本（/cost 命令）。
func (e *Engine) CostBoard() string {
	s := e.CostSnapshot()
	var b strings.Builder
	b.WriteString("成本看板\n")
	fmt.Fprintf(&b, "  模型      %s\n", orDash(s.Model))
	fmt.Fprintf(&b, "  调用轮数  %d\n", s.Turns)
	fmt.Fprintf(&b, "  输入      %s（其中缓存命中 %s）\n", tokNum(s.TokIn), tokNum(s.Cached))
	fmt.Fprintf(&b, "  输出      %s\n", tokNum(s.TokOut))
	fmt.Fprintf(&b, "  缓存写入  %s\n", tokNum(s.CacheW))
	fmt.Fprintf(&b, "  命中率    %s（最近一轮 %s）\n", pct(s.HitRate), pct(s.LastHit))
	fmt.Fprintf(&b, "  估算费用  %s\n", core.Money(s.Cost))
	fmt.Fprintf(&b, "  无缓存需  %s（缓存省下 %s）\n", core.Money(s.CostPlain), core.Money(s.CostPlain-s.Cost))
	if s.RouterOn {
		fmt.Fprintf(&b, "  模型路由  开启%s\n", fastPart(s.FastModel))
		if s.FastTurns > 0 {
			fmt.Fprintf(&b, "  轻任务    %d 次，输入 %s / 输出 %s，花费 %s（走主模型需多花 %s）\n",
				s.FastTurns, tokNum(s.FastTokIn), tokNum(s.FastTokOut), core.Money(s.FastCost), core.Money(s.SavedByFast))
		} else {
			b.WriteString("  轻任务    本轮还没有分流记录\n")
		}
	} else {
		b.WriteString("  模型路由  关闭（全部使用主模型）\n")
	}
	if s.SubTurns > 0 {
		fmt.Fprintf(&b, "  子代理    %d 轮，输入 %s / 输出 %s\n", s.SubTurns, tokNum(s.SubTokIn), tokNum(s.SubTokOut))
	}
	if !s.Precise {
		b.WriteString("  计价说明  未配置自定义单价，按保守默认价估算；设置 → 预算可填输入/输出单价。\n")
	} else {
		b.WriteString("  计价说明  按设置里的自定义单价估算；缓存按输入价的 10% 计。\n")
	}
	if s.HitRate == 0 && s.Turns > 0 {
		b.WriteString("  优化建议  命中率为 0：确认系统提示不含可变内容，且端点支持 prompt caching。\n")
	}
	return b.String()
}

// pct 格式化百分比。
func pct(v float64) string {
	if v <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.0f%%", v*100)
}

// tokNum 用千分位展示 token 数。
func tokNum(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%d", n)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（跟随默认）"
	}
	return s
}
