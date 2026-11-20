package agent

import (
	"strings"
	"testing"
	"time"
)

func TestToolStatsAggregateAndSort(t *testing.T) {
	e := New(nil, "m", t.TempDir())
	e.recordTool("read_file", 100*time.Millisecond, false)
	e.recordTool("read_file", 300*time.Millisecond, true)
	e.recordTool("write_file", 50*time.Millisecond, false)
	e.recordTool("grep", 900*time.Millisecond, false)

	stats := e.ToolStats()
	if len(stats) != 3 {
		t.Fatalf("应有 3 个工具，实际 %d", len(stats))
	}
	if stats[0].Name != "grep" {
		t.Fatalf("应按总耗时降序，首位应为 grep，实际 %s", stats[0].Name)
	}
	var read ToolStat
	for _, st := range stats {
		if st.Name == "read_file" {
			read = st
		}
	}
	if read.Calls != 2 || read.Fails != 1 {
		t.Fatalf("read_file 统计不对: %+v", read)
	}
	if read.Total != 400*time.Millisecond || read.Max != 300*time.Millisecond {
		t.Fatalf("耗时累计不对: %+v", read)
	}
	if read.Avg() != 200*time.Millisecond {
		t.Fatalf("均值不对: %v", read.Avg())
	}
}

func TestBreakLineAndThroughput(t *testing.T) {
	e := New(nil, "m", t.TempDir())
	if !strings.Contains(e.BreakLine(), "还没有") {
		t.Fatalf("空统计应给出提示: %s", e.BreakLine())
	}
	if !strings.Contains(e.Throughput(), "尚未") {
		t.Fatalf("空统计应给出提示: %s", e.Throughput())
	}

	e.recordModel(2*time.Second, 800) // 2s 出 800 token
	e.recordTool("read_file", 2*time.Second, false)

	line := e.BreakLine()
	if !strings.Contains(line, "模型 2.0s（50%）") || !strings.Contains(line, "工具 2.0s（50%）") {
		t.Fatalf("占比不对: %s", line)
	}
	tp := e.Throughput()
	if !strings.Contains(tp, "400.0 token/s") {
		t.Fatalf("速度算错: %s", tp)
	}
	if !strings.Contains(tp, "最慢一轮 2.0s") {
		t.Fatalf("最慢一轮不对: %s", tp)
	}
}

func TestToolStatsLineLimit(t *testing.T) {
	e := New(nil, "m", t.TempDir())
	if !strings.Contains(e.ToolStatsLine(3), "还没有") {
		t.Fatal("无统计时应提示")
	}
	for _, name := range []string{"a", "b", "c"} {
		e.recordTool(name, 10*time.Millisecond, false)
	}
	got := e.ToolStatsLine(2)
	if n := strings.Count(got, "\n"); n != 1 {
		t.Fatalf("limit=2 应输出 2 行，实际换行 %d 次: %s", n, got)
	}
}
