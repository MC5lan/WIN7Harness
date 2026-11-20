package coreutil

import (
	"testing"
	"time"
)

func TestMetricsPercentileAndSnapshot(t *testing.T) {
	m := NewMetrics(10)
	for i := 1; i <= 10; i++ {
		m.Observe("tool.read", time.Duration(i)*time.Millisecond, i != 10)
	}
	m.Bump("task.ok")
	s := m.Snapshot()
	if len(s.Stats) != 1 {
		t.Fatalf("应有 1 个指标，实际 %d", len(s.Stats))
	}
	st := s.Stats[0]
	if st.Count != 10 || st.Fail != 1 {
		t.Fatalf("计数不对：%+v", st)
	}
	if st.P50 != 5 || st.P95 != 10 || st.Max != 10 {
		t.Fatalf("分位数不对：%+v", st)
	}
	if s.Counters["task.ok"] != 1 {
		t.Fatal("计数器未生效")
	}
	if s.Goroutines <= 0 {
		t.Fatal("应能读到内存/协程画像")
	}
}

func TestMetricsRingBound(t *testing.T) {
	m := NewMetrics(8)
	for i := 0; i < 100; i++ {
		m.Observe("x", time.Millisecond, true)
	}
	if n := len(m.samples); n > 8 {
		t.Fatalf("样本应受上限约束，实际 %d", n)
	}
}
