package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// 耗时与 token 归因。
//
// 目的很实际：用户问"为什么这一轮这么慢 / 这么贵"，得能立刻答上来——
// 是模型慢还是工具慢、慢在哪个工具、每轮多少 token。
// 统计只存在内存里，随引擎生命周期走，不落盘（统计本身不该变成负担）。

// ToolStat 单个工具的累计统计。
type ToolStat struct {
	Name  string
	Calls int
	Fails int
	Total time.Duration
	Max   time.Duration
}

// Avg 平均耗时。
func (s ToolStat) Avg() time.Duration {
	if s.Calls == 0 {
		return 0
	}
	return s.Total / time.Duration(s.Calls)
}

// recordTool 记一次工具调用的耗时与成败。
func (e *Engine) recordTool(name string, d time.Duration, failed bool) {
	if name == "" {
		return
	}
	e.toolMu.Lock()
	defer e.toolMu.Unlock()
	if e.toolStats == nil {
		e.toolStats = make(map[string]*ToolStat)
	}
	st, ok := e.toolStats[name]
	if !ok {
		st = &ToolStat{Name: name}
		e.toolStats[name] = st
	}
	st.Calls++
	if failed {
		st.Fails++
	}
	st.Total += d
	if d > st.Max {
		st.Max = d
	}
	e.toolTotalMs += d.Milliseconds()
}

// recordModel 记一轮模型调用的耗时，用于算"模型慢还是工具慢"与每秒 token。
func (e *Engine) recordModel(d time.Duration, u int) {
	e.toolMu.Lock()
	defer e.toolMu.Unlock()
	e.modelTotalMs += d.Milliseconds()
	e.modelTokens += u
	if d > time.Duration(e.slowestModelMs)*time.Millisecond {
		e.slowestModelMs = d.Milliseconds()
	}
}

// ToolStats 返回按总耗时降序排列的工具统计。
func (e *Engine) ToolStats() []ToolStat {
	e.toolMu.Lock()
	out := make([]ToolStat, 0, len(e.toolStats))
	for _, st := range e.toolStats {
		out = append(out, *st)
	}
	e.toolMu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total == out[j].Total {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Total > out[j].Total
	})
	return out
}

// BreakLine 生成耗时归因的一行摘要：模型 vs 工具各占多久。
func (e *Engine) BreakLine() string {
	e.toolMu.Lock()
	modelMs, toolMs := e.modelTotalMs, e.toolTotalMs
	e.toolMu.Unlock()
	total := modelMs + toolMs
	if total == 0 {
		return "还没有可统计的调用"
	}
	pct := func(v int64) float64 { return float64(v) * 100 / float64(total) }
	return fmt.Sprintf("模型 %.1fs（%.0f%%） / 工具 %.1fs（%.0f%%）",
		float64(modelMs)/1000, pct(modelMs), float64(toolMs)/1000, pct(toolMs))
}

// Throughput 生成速度摘要：每秒输出多少 token、最慢一轮多久。
func (e *Engine) Throughput() string {
	e.toolMu.Lock()
	modelMs, toks, slowest := e.modelTotalMs, e.modelTokens, e.slowestModelMs
	e.toolMu.Unlock()
	if modelMs <= 0 {
		return "尚未完成模型调用"
	}
	speed := float64(toks) * 1000 / float64(modelMs)
	s := fmt.Sprintf("平均 %.1f token/s（最慢一轮 %.1fs）", speed, float64(slowest)/1000)
	return s
}

// ToolStatsLine 生成 Top N 工具耗时列表，供 /stats 与 /status 使用。
func (e *Engine) ToolStatsLine(limit int) string {
	stats := e.ToolStats()
	if len(stats) == 0 {
		return "本轮还没有工具调用"
	}
	if limit > 0 && len(stats) > limit {
		stats = stats[:limit]
	}
	var b strings.Builder
	for i, st := range stats {
		if i > 0 {
			b.WriteString("\n")
		}
		fail := ""
		if st.Fails > 0 {
			fail = fmt.Sprintf("，失败 %d", st.Fails)
		}
		b.WriteString(fmt.Sprintf("  %-14s %3d 次  共 %5.1fs  均 %4.0fms  峰 %4.0fms%s",
			st.Name, st.Calls, st.Total.Seconds(),
			float64(st.Avg().Milliseconds()), float64(st.Max.Milliseconds()), fail))
	}
	return b.String()
}

// ModelToolMs 返回模型与工具各自的累计耗时（毫秒），供界面画占比条。
func (e *Engine) ModelToolMs() (modelMs, toolMs int64) {
	e.toolMu.Lock()
	defer e.toolMu.Unlock()
	return e.modelTotalMs, e.toolTotalMs
}

// StatItems 返回 Top N 工具耗时的结构化统计，供表格渲染使用。
// 与 ToolStatsLine 相比，这里不做空格对齐——比例字体下空格对齐必然错位，
// 对齐交给界面按列处理。
func (e *Engine) StatItems(limit int) []ToolStat {
	stats := e.ToolStats()
	if limit > 0 && len(stats) > limit {
		stats = stats[:limit]
	}
	return stats
}
