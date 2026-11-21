package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/tools"
)

// pendingCall 一次待执行的工具调用。
type pendingCall struct {
	id, name, args string
}

// 并发度上限：本机 IO 型任务，4 路足够，避免把工具输出刷屏。
const maxParallelTools = 4

// execBatch 执行一批工具调用，返回与 calls 顺序一致的结果。
//
// 规则：以“写类工具”为分界，连续只读段内并行；写类内联串行；段间严格按调用顺序。
// 此前只读 goroutine 与内联写并行：同批 [read_file(X), write_file(X)] 时，
// 写可能先于读完成，模型读到的是写后内容（读了新文件却不自知）。
// 现在写必然等前面所有并行段全部读完后才执行。
func (e *Engine) execBatch(ctx context.Context, calls []pendingCall) ([]string, []string, []time.Duration) {
	outs := make([]string, len(calls))
	details := make([]string, len(calls))
	durs := make([]time.Duration, len(calls))
	if len(calls) == 0 {
		return outs, details, durs
	}

	sem := make(chan struct{}, maxParallelTools)
	i := 0
	for i < len(calls) {
		if ctx.Err() != nil {
			outs[i] = "错误：任务已停止，未执行"
			i++
			continue
		}
		// 连续只读段：并行执行（仍按原始下标回填结果）
		if tools.IsParallelSafe(calls[i].name) {
			j := i
			for j < len(calls) && tools.IsParallelSafe(calls[j].name) && ctx.Err() == nil {
				j++
			}
			seg := calls[i:j]
			var wg sync.WaitGroup
			for k := range seg {
				wg.Add(1)
				sem <- struct{}{}
				k := k
				core.SafeGo("agent/batch.go:read", func() {
					defer wg.Done()
					defer func() { <-sem }()
					begin := time.Now()
					outs[i+k], details[i+k] = e.execTool(seg[k].name, seg[k].args)
					durs[i+k] = time.Since(begin)
				})
			}
			wg.Wait()
			i = j
			continue
		}
		// 写类工具：内联串行，保证顺序语义与确认流程不被并发打乱
		begin := time.Now()
		outs[i], details[i] = e.execTool(calls[i].name, calls[i].args)
		durs[i] = time.Since(begin)
		i++
	}

	// 统计落在批量执行的出口：并行与串行两条路径都能覆盖到
	for i, c := range calls {
		e.recordTool(c.name, durs[i], strings.HasPrefix(outs[i], "错误："))
	}
	return outs, details, durs
}

// projectDocBlock 注入项目约定文件（AGENTS.md / CLAUDE.md 等）。
// 每轮重新读取，用户中途修改约定可立即生效。
func projectDocBlock(workDir string) string {
	return core.ProjectDocBlock(workDir)
}
