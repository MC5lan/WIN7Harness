package coreutil

import (
	"testing"
	"time"
)

func spans(specs ...string) []ToolSpan {
	out := make([]ToolSpan, 0, len(specs))
	for _, s := range specs {
		switch s {
		case "ok":
			out = append(out, ToolSpan{Done: true, Elapsed: time.Second})
		case "err":
			out = append(out, ToolSpan{Done: true, Fail: true, Elapsed: 2 * time.Second})
		case "run":
			out = append(out, ToolSpan{Done: false})
		}
	}
	return out
}

// ST39：折叠边界——只有连续 ≥3 条"已结束"才折，运行中的卡绝不折。
func TestFoldRunsV124(t *testing.T) {
	// 少于三条：不折，避免把两条卡也藏起来。
	if got := FoldRuns(spans("ok", "ok"), nil); len(got) != 0 {
		t.Fatalf("两条不应折叠，实际 %+v", got)
	}
	if got := FoldRuns(nil, nil); len(got) != 0 {
		t.Fatalf("空消息不应折叠，实际 %+v", got)
	}

	// 三条全部结束：折成一组，长度 3、耗时累加、失败数正确。
	got := FoldRuns(spans("ok", "err", "ok"), nil)
	if len(got) != 1 || got[0].Start != 0 || got[0].Len != 3 {
		t.Fatalf("三条应折成 1 组 3 条，实际 %+v", got)
	}
	if got[0].Fails != 1 {
		t.Fatalf("失败数 = %d, 期望 1", got[0].Fails)
	}
	if got[0].Dur != 4*time.Second {
		t.Fatalf("耗时 = %v, 期望 4s", got[0].Dur)
	}

	// 运行中的卡会截断分组：ok,ok,run,ok,ok → 前后各 2 条，都不够 3，全部不折。
	if got := FoldRuns(spans("ok", "ok", "run", "ok", "ok"), nil); len(got) != 0 {
		t.Fatalf("被运行中卡截断后不应折叠，实际 %+v", got)
	}

	// 五条结束 + 一条运行中：前五条折成一组，最后一条运行中保持可见。
	got = FoldRuns(spans("ok", "ok", "ok", "ok", "ok", "run"), nil)
	if len(got) != 1 || got[0].Len != 5 {
		t.Fatalf("前 5 条应折成 1 组，实际 %+v", got)
	}

	// 两段各三条，中间隔着运行中的卡：两段分别折。
	got = FoldRuns(spans("ok", "ok", "ok", "run", "ok", "ok", "ok"), nil)
	if len(got) != 2 || got[0].Start != 0 || got[1].Start != 4 {
		t.Fatalf("两段应各自折叠，实际 %+v", got)
	}
}

// ST40：手动展开过的组不再折叠，且展开状态只影响对应那一组。
func TestFoldRunsOpenStateV124(t *testing.T) {
	all := spans("ok", "ok", "ok", "run", "ok", "err", "ok")

	// 展开第一组（起始 0）：只剩第二组（起始 4）折叠。
	got := FoldRuns(all, map[int]bool{0: true})
	if len(got) != 1 || got[0].Start != 4 {
		t.Fatalf("展开第一组后应只折第二组，实际 %+v", got)
	}

	// 两组都展开：一条都不折，用户能逐条看到。
	if got := FoldRuns(all, map[int]bool{0: true, 4: true}); len(got) != 0 {
		t.Fatalf("两组都展开时不应折叠，实际 %+v", got)
	}

	// open 里的下标不在组起点上（如 1）：不应误伤折叠行为。
	got = FoldRuns(all, map[int]bool{1: true})
	if len(got) != 2 {
		t.Fatalf("非组起点的展开标记不应影响折叠，实际 %+v", got)
	}
}
