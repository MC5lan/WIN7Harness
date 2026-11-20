package coreutil

import (
	"runtime"
	"sort"
	"sync"
	"time"
)

// 运行指标：v1.28 之前"Win7 老机到底慢在哪"只能靠感觉。
// 这里记少量样本（环形，够算分位数就行），并把内存画像一起吐出来，
// 既能 /stats 看，也能打进诊断包。

// Sample 一次耗时采样。
type Sample struct {
	Name string
	MS   int64
	OK   bool
	At   time.Time
}

// Stat 单个指标的聚合结果。
type Stat struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Fail  int    `json:"fail"`
	P50   int64  `json:"p50_ms"`
	P95   int64  `json:"p95_ms"`
	Max   int64  `json:"max_ms"`
}

// Snapshot 一次性指标快照。
type MetricsSnapshot struct {
	At         time.Time      `json:"at"`
	Stats      []Stat         `json:"stats"`
	Counters   map[string]int `json:"counters"`
	HeapMB     int            `json:"heap_mb"`
	SysMB      int            `json:"sys_mb"`
	StackMB    int            `json:"stack_mb"`
	Goroutines int            `json:"goroutines"`
	GC         uint32         `json:"gc_cycles"`
}

// Metrics 轻量指标收集器，并发安全。
type Metrics struct {
	mu       sync.Mutex
	max      int
	samples  []Sample
	counters map[string]int
}

// NewMetrics 创建收集器，max 为总样本上限（<=0 取 2048）。
func NewMetrics(max int) *Metrics {
	if max <= 0 {
		max = 2048
	}
	return &Metrics{max: max, counters: map[string]int{}}
}

// Observe 记录一次耗时。
func (m *Metrics) Observe(name string, d time.Duration, ok bool) {
	ms := d.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples = append(m.samples, Sample{Name: name, MS: ms, OK: ok, At: time.Now()})
	if len(m.samples) > m.max {
		// 丢最旧的一半，避免每来一条就搬一次整个切片。
		m.samples = append([]Sample(nil), m.samples[len(m.samples)-m.max/2:]...)
	}
}

// Bump 计数器 +1。
func (m *Metrics) Bump(key string) {
	m.mu.Lock()
	m.counters[key]++
	m.mu.Unlock()
}

// Counter 读计数器。
func (m *Metrics) Counter(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[key]
}

// Snapshot 汇总分位数与内存画像。
func (m *Metrics) Snapshot() MetricsSnapshot {
	m.mu.Lock()
	bys := map[string][]int64{}
	fails := map[string]int{}
	for _, s := range m.samples {
		bys[s.Name] = append(bys[s.Name], s.MS)
		if !s.OK {
			fails[s.Name]++
		}
	}
	counters := make(map[string]int, len(m.counters))
	for k, v := range m.counters {
		counters[k] = v
	}
	m.mu.Unlock()

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	snap := MetricsSnapshot{
		At:         time.Now(),
		Counters:   counters,
		HeapMB:     int(ms.HeapAlloc / (1 << 20)),
		SysMB:      int(ms.Sys / (1 << 20)),
		StackMB:    int(ms.StackInuse / (1 << 20)),
		Goroutines: runtime.NumGoroutine(),
		GC:         ms.NumGC,
	}
	for name, list := range bys {
		sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
		st := Stat{
			Name:  name,
			Count: len(list),
			Fail:  fails[name],
			P50:   percentile(list, 50),
			P95:   percentile(list, 95),
			Max:   list[len(list)-1],
		}
		snap.Stats = append(snap.Stats, st)
	}
	sort.Slice(snap.Stats, func(i, j int) bool { return snap.Stats[i].Name < snap.Stats[j].Name })
	return snap
}

// percentile 用最近秩法算分位数，输入必须已排序。
func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	idx := (p*len(sorted) + 99) / 100 // 向上取整的最近秩
	if idx < 1 {
		idx = 1
	}
	if idx > len(sorted) {
		idx = len(sorted)
	}
	return sorted[idx-1]
}

// MetricsText 给 /stats 用的纯文本表格。
func MetricsText(s MetricsSnapshot) string {
	out := "运行指标（采样 " + s.At.Format("15:04:05") + "）\n"
	out += "内存：堆 " + itoa(int64(s.HeapMB)) + " MB · 系统 " + itoa(int64(s.SysMB)) + " MB · 栈 " + itoa(int64(s.StackMB)) +
		" MB · GC " + itoa(int64(int(s.GC))) + " 次 · 协程 " + itoa(int64(s.Goroutines)) + "\n"
	if len(s.Stats) == 0 {
		out += "耗时：暂无采样（跑几次任务再看）\n"
	} else {
		out += "耗时   名称                 次数  失败   P50    P95    最大\n"
		for _, st := range s.Stats {
			out += "       " + padRight(st.Name, 20) + " " + padLeft(itoa(int64(st.Count)), 5) + " " +
				padLeft(itoa(int64(st.Fail)), 5) + " " + padLeft(itoa(int64(int(st.P50))), 5) + " " +
				padLeft(itoa(int64(int(st.P95))), 6) + " " + padLeft(itoa(int64(int(st.Max))), 6) + "\n"
		}
	}
	if len(s.Counters) > 0 {
		keys := make([]string, 0, len(s.Counters))
		for k := range s.Counters {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out += "计数："
		for i, k := range keys {
			if i > 0 {
				out += " · "
			}
			out += k + "=" + itoa(int64(s.Counters[k]))
		}
		out += "\n"
	}
	return out
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	if len(s) > n {
		return s[:n]
	}
	return s
}

func padLeft(s string, n int) string {
	for len(s) < n {
		s = " " + s
	}
	return s
}
