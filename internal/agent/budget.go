package agent

import (
	"fmt"

	"win7harness/internal/core"
)

// BudgetState 是本会话的用量与预算快照。
type BudgetState struct {
	TokIn, TokOut, Turns     int
	FastCost, SubCost        float64
	CostIn, CostOut, CostUSD float64
	LimitCost                float64
	LimitTokens              int
	UsedRatio                float64 // 已达额度比例，取两项中较大值
	Exceeded                 bool
	Warn                     bool
	Reason                   string
}

// calcBudget 纯计算部分，便于单测。
func calcBudget(in, out, turns int, priceIn, priceOut, limitCost float64, limitTok int, warn bool) BudgetState {
	bs := BudgetState{
		TokIn: in, TokOut: out, Turns: turns,
		CostIn:      float64(in) / 1e6 * priceIn,
		CostOut:     float64(out) / 1e6 * priceOut,
		LimitCost:   limitCost,
		LimitTokens: limitTok,
	}
	bs.CostUSD = bs.CostIn + bs.CostOut
	applyLimit(&bs, limitCost, limitTok, warn)
	return bs
}

// applyLimit 统一按“累计 token / 累计花费”两个维度计算 UsedRatio / Warn / Exceeded。
func applyLimit(bs *BudgetState, limitCost float64, limitTok int, warn bool) {
	totalTok := int64(bs.TokIn) + int64(bs.TokOut)
	ratio := float64(0)
	if limitTok > 0 {
		ratio = float64(totalTok) / float64(limitTok)
	}
	if limitCost > 0 {
		if r := bs.CostUSD / limitCost; r > ratio {
			ratio = r
		}
	}
	bs.UsedRatio = ratio
	bs.Warn = warn && bs.UsedRatio >= 0.8 && bs.UsedRatio < 1
	bs.Exceeded = bs.UsedRatio >= 1
	if bs.Exceeded {
		switch {
		case limitCost > 0 && limitTok > 0:
			bs.Reason = fmt.Sprintf("已达预算上限（%.4f 美元 / %d token）", limitCost, limitTok)
		case limitCost > 0:
			bs.Reason = fmt.Sprintf("已达花费上限 %.4f 美元", limitCost)
		default:
			bs.Reason = fmt.Sprintf("已达 token 上限 %d", limitTok)
		}
	}
}

// Budget 汇总当前用量、单价与限额，供界面顶栏与 /status 展示。
// 覆盖全部三桶：主对话 + 快速模型（压缩/只读子代理）+ 子代理——
// 此前只算主桶，开了路由的用户压缩/子代理消耗无限额可言。
func (e *Engine) Budget() BudgetState {
	e.usageMu.Lock()
	in, out, turns := e.tokIn, e.tokOut, e.turns
	fin, fout, ft := e.fastTokIn, e.fastTokOut, e.fastTurns
	sin, sout, st := e.subTokIn, e.subTokOut, e.subTurns
	sfi, sfo := e.subFastTokIn, e.subFastTokOut
	// 读引擎自己的配置快照：UI 保存设置时不会与 Agent 并发读写 Config。
	priceIn, priceOut := e.PriceIn, e.PriceOut
	limitCost, limitTok := e.MaxCost, e.BudgetTokens
	warn := e.BudgetWarn
	e.usageMu.Unlock()

	// 主对话（沿用原语义，priceIn/priceOut 为主模型单价）
	bs := calcBudget(in, out, turns, priceIn, priceOut, limitCost, limitTok, warn)

	// 快速桶（压缩/只读子代理）
	fastCost := 0.0
	if e.FastModel != "" && (fin+fout) > 0 {
		fastCost = core.Cost(e.FastModel, fin, 0, fout)
	}
	// 子代理桶：快速路由部分按 FastModel 价，其余按主模型价
	sMainIn, sMainOut := sin-sfi, sout-sfo
	if sMainIn < 0 {
		sMainIn = 0
	}
	if sMainOut < 0 {
		sMainOut = 0
	}
	subCost := core.Cost(e.Model, sMainIn, 0, sMainOut)
	if e.FastModel != "" && (sfi+sfo) > 0 {
		subCost += core.Cost(e.FastModel, sfi, 0, sfo)
	}

	bs.TokIn = in + fin + sin
	bs.TokOut = out + fout + sout
	bs.Turns = turns + ft + st
	bs.FastCost = fastCost
	bs.SubCost = subCost
	bs.CostUSD += fastCost + subCost
	applyLimit(&bs, limitCost, limitTok, warn)
	return bs
}

// SetBudgetLimits 更新引擎的预算快照（/budget 命令用）。
// 与 Budget 共用 usageMu，避免运行中改限额的读写竞争。
func (e *Engine) SetBudgetLimits(limitCost float64, limitTokens int, warn bool) {
	e.usageMu.Lock()
	e.MaxCost = limitCost
	e.BudgetTokens = limitTokens
	e.BudgetWarn = warn
	e.usageMu.Unlock()
}

// CheckBudget 在每轮模型调用后调用，返回是否应停止本轮循环。
func (e *Engine) CheckBudget() (stop bool, note string) {
	bs := e.Budget()
	if bs.Exceeded {
		return true, bs.Reason + "，已自动停止继续调用模型。调整上限可发 /budget，或直接发送下一条消息继续。"
	}
	if bs.Warn {
		return false, fmt.Sprintf("预算已用 %.0f%%，本轮累计 %.4f 美元 / %d token", bs.UsedRatio*100, bs.CostUSD, bs.TokIn+bs.TokOut)
	}
	return false, ""
}

// BudgetLine 一行摘要，用于状态栏。
func (e *Engine) BudgetLine() string {
	bs := e.Budget()
	line := fmt.Sprintf("%d 轮 · %d↓ %d↑ token · $%.4f", bs.Turns, bs.TokIn, bs.TokOut, bs.CostUSD)
	if bs.Exceeded {
		line += " · 已超预算"
	} else if bs.UsedRatio > 0 {
		line += fmt.Sprintf(" · 已用额度 %.0f%%", bs.UsedRatio*100)
	}
	return line
}
