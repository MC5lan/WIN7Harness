package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v1.29 C 批单测：把原本埋在 app 包（windows build tag，开发机跑不了）里的
// 分支密集逻辑抽到 core 后，这里逐条验证——这类"敲错一个字就走另一条路"的地方，
// 靠手工点最容易漏。队列/设备表/指标的基础用例已在 v129_test.go，这里不重复。

func TestUpdateCmdParse(t *testing.T) {
	cases := []struct {
		in    string
		mode  string
		force bool
	}{
		{"", "", false},
		{"apply", "apply", false},
		{"apply force", "apply", true},
		{"APPLY FORCE", "apply", true},
		{"装 强制", "apply", true},
		{"替换 -f", "apply", true},
		{"status", "", false},
		{"状态", "", false},
		{"随便写的", "", false},
	}
	for _, c := range cases {
		mode, force := UpdateCmd(c.in)
		if mode != c.mode || force != c.force {
			t.Fatalf("UpdateCmd(%q) = (%q,%v)，期望 (%q,%v)", c.in, mode, force, c.mode, c.force)
		}
	}
}

func TestQueueCmdParse(t *testing.T) {
	cases := map[string]string{
		"":      "list",
		"list":  "list",
		"clear": "clear",
		"清空":    "clear",
		"run":   "run",
		"开始":    "run",
		"别的":    "list",
	}
	for in, want := range cases {
		if got := QueueCmd(in); got != want {
			t.Fatalf("QueueCmd(%q)=%q，期望 %q", in, got, want)
		}
	}
}

func TestDeviceCmdParse(t *testing.T) {
	cases := []struct {
		in     string
		action string
		target string
	}{
		{"", "list", ""},
		{"list", "list", ""},
		{"prune", "prune", ""},
		{"清理", "prune", ""},
		{"revoke 小马手机", "revoke", "小马手机"},
		// 只给名字也当吊销：手机上少打一个词不该变成"什么都没发生"
		{"小马手机", "revoke", "小马手机"},
	}
	for _, c := range cases {
		a, tg := DeviceCmd(c.in)
		if a != c.action || tg != c.target {
			t.Fatalf("DeviceCmd(%q)=(%q,%q)，期望 (%q,%q)", c.in, a, tg, c.action, c.target)
		}
	}
}

func TestStopReport(t *testing.T) {
	// 什么都没在跑时必须说实话，不能回一句"已停止"让用户以为活干完了。
	if msg, ok := StopReport(false, 0); ok || !strings.Contains(msg, "没有") {
		t.Fatalf("空状态下不该谎报已停止：%q %v", msg, ok)
	}
	if msg, ok := StopReport(true, 0); !ok {
		t.Fatalf("停了生成应算有效停止：%q", msg)
	}
	msg, ok := StopReport(false, 3)
	if !ok || !strings.Contains(msg, "3") {
		t.Fatalf("应说明队列里清掉几个：%q", msg)
	}
	msg, ok = StopReport(true, 2)
	if !ok || !strings.Contains(msg, "2") {
		t.Fatalf("两件事都要说清：%q", msg)
	}
}

func TestPlanToggleText(t *testing.T) {
	if got := PlanToggleText("夜间备份", true); !strings.Contains(got, "启用") || !strings.Contains(got, "夜间备份") {
		t.Fatalf("启用文案不对：%q", got)
	}
	if got := PlanToggleText("夜间备份", false); !strings.Contains(got, "暂停") {
		t.Fatalf("暂停文案不对：%q", got)
	}
	long := strings.Repeat("很长的任务名", 10)
	if got := PlanToggleText(long, true); len([]rune(got)) > 32 {
		t.Fatalf("长标题应被截断，实际 %d 字：%q", len([]rune(got)), got)
	}
}

func TestProfileTiers(t *testing.T) {
	cases := []struct {
		mb   int
		tier string
	}{
		{512, "2G"},
		{2048, "2G"},
		{4096, "4G"},
		{8192, "8G+"},
		{0, "未知"},
	}
	for _, c := range cases {
		p := profileFor(c.mb)
		if p.Tier != c.tier {
			t.Fatalf("profileFor(%d).Tier=%q，期望 %q", c.mb, p.Tier, c.tier)
		}
		if p.HistoryMax <= 0 || p.Workers <= 0 || p.BigFileMB <= 0 || p.CacheMB <= 0 {
			t.Fatalf("profileFor(%d) 有零值字段：%+v", c.mb, p)
		}
		if !strings.Contains(p.Text(), c.tier) {
			t.Fatalf("Text() 没体现档位：%q", p.Text())
		}
	}
	// 档位必须单调：内存越大，允许的预算不能反而更小（否则等于越省越费）。
	prev := -1
	for _, mb := range []int{1024, 2048, 4096, 8192, 16384} {
		p := profileFor(mb)
		if prev >= 0 && p.CacheMB < prev {
			t.Fatalf("内存 %dMB 的缓存预算反而更低：%d < %d", mb, p.CacheMB, prev)
		}
		prev = p.CacheMB
	}
}

func TestMemProfileMatchesTier(t *testing.T) {
	// 自动档位与手动低内存开关必须同一套口径，否则 /memory 说的和实际用的会不一样。
	m := MemProfile()
	if m.IndexFiles <= 0 || m.ToolOutput <= 0 || m.AtRefBudget <= 0 {
		t.Fatalf("阈值不能为 0：%+v", m)
	}
	name := m.ModeName()
	switch DetectProfile().Tier {
	case "2G":
		if !strings.Contains(name, "低内存") {
			t.Fatalf("2G 机器应自动收紧，实际档位 %q", name)
		}
	case "4G":
		if !strings.Contains(name, "中等") {
			t.Fatalf("4G 机器档位不对：%q", name)
		}
	default:
		if !strings.Contains(name, "标准") && !strings.Contains(name, "低内存") {
			t.Fatalf("档位描述异常：%q", name)
		}
	}
	// 收紧档必须比标准档更省，不然"自动收紧"是假的。
	low := lowTierLimits()
	std := stdLimits()
	if low.IndexFiles >= std.IndexFiles || low.ToolOutput >= std.ToolOutput || low.SubTasks > std.SubTasks {
		t.Fatalf("低内存档没有真的更省：low=%+v std=%+v", low, std)
	}
	if d := m.Describe(); !strings.Contains(d, "档位") {
		t.Fatalf("Describe 应给出可读说明：%q", d)
	}
}

func TestConfigQuarantineOnBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	_ = os.WriteFile(filepath.Join(dir, "config.json"), []byte("{坏掉的 json"), 0o644)

	_ = TakeConfigLoadError() // 清掉上一个用例可能留下的状态
	cfg := Load()
	if cfg == nil {
		t.Fatal("坏配置也要返回可用默认值，不能返回 nil")
	}
	if cfg.Version == "" {
		t.Fatal("默认配置应带上当前版本号")
	}
	// 原文件应改名留证，用户才不会以为"设置被吞了"。
	entries, _ := os.ReadDir(dir)
	hasBad := false
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bad-") {
			hasBad = true
		}
	}
	if !hasBad {
		t.Fatalf("坏配置应被备份留证，目录内：%v", entries)
	}
	if msg := TakeConfigLoadError(); msg == "" {
		t.Fatal("应暴露配置损坏原因给界面")
	}
	if msg := TakeConfigLoadError(); msg != "" {
		t.Fatal("原因读后即清，不能反复提示")
	}
}

func TestScheduleLoadReportsError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "schedule.json")
	_ = os.WriteFile(p, []byte("not json"), 0o644)
	st := LoadSchedules(p)
	if st == nil {
		t.Fatal("坏清单也要返回可用结构")
	}
	if st.LoadErr == "" {
		t.Fatal("定时清单损坏必须留原因（不能静默清空，用户会以为任务凭空消失）")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) == 0 {
		t.Fatal("应留下备份文件")
	}
	// 文件不存在时不该报错——首次启动的常态。
	st2 := LoadSchedules(filepath.Join(dir, "nope.json"))
	if st2 == nil || st2.LoadErr != "" {
		t.Fatalf("文件不存在应返回空表且无错：%+v", st2)
	}
}
