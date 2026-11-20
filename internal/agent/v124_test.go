package agent

import (
	"strings"
	"testing"
)

// 路由与预算这层没有网络依赖，全部用导出钩子驱动真实记账路径：
// 走的是 addUsageFor 同一个函数，断言的是分桶结果，不是"字段存在"。

// ST36：路由开关与快速模型都要真的改变选中的模型，任一条件不满足必须回落主模型。
func TestModelRouterV124(t *testing.T) {
	e := &Engine{Model: "main-big"}

	// 未开启路由：所有任务都用主模型。
	e.SetRouter(false, "fast-small")
	for _, job := range []string{JobMain, JobCompact, JobSubReadOnly, JobSubWrite} {
		if got := e.ModelFor(job); got != "main-big" {
			t.Fatalf("路由关闭时 %s 应为 main-big，实际 %q", job, got)
		}
	}
	if !strings.Contains(e.RouterSummary(), "关闭") {
		t.Fatalf("摘要应提示关闭: %s", e.RouterSummary())
	}

	// 开启但未配快速模型：仍全用主模型，且摘要要点明这一点（否则用户以为省了钱）。
	e.SetRouter(true, "")
	if got := e.ModelFor(JobCompact); got != "main-big" {
		t.Fatalf("未配快速模型时应回落主模型，实际 %q", got)
	}
	if !strings.Contains(e.RouterSummary(), "未指定快速模型") {
		t.Fatalf("摘要应提示未指定快速模型: %s", e.RouterSummary())
	}

	// 快速模型与主模型同名：等价于没路由，不能白记一笔 fast 桶。
	e.SetRouter(true, "main-big")
	if _, m := e.ClientFor(JobCompact); m != "main-big" {
		t.Fatalf("同名快速模型应回落主模型，实际 model=%q", m)
	}

	// 正常路由：压缩与只读子代理走快速模型，主对话与写文件子代理仍走主模型。
	e.SetRouter(true, "fast-small")
	cases := map[string]string{
		JobMain:        "main-big",
		JobSubWrite:    "main-big",
		JobCompact:     "fast-small",
		JobSubReadOnly: "fast-small",
	}
	for job, want := range cases {
		if got := e.ModelFor(job); got != want {
			t.Fatalf("%s 应路由到 %q，实际 %q", job, want, got)
		}
	}
	if _, m := e.ClientFor(JobCompact); m != "fast-small" {
		t.Fatalf("ClientFor 返回模型 = %q, 期望 fast-small", m)
	}
	sum := e.RouterSummary()
	if !strings.Contains(sum, "main-big") || !strings.Contains(sum, "fast-small") {
		t.Fatalf("摘要应同时显示两个模型: %s", sum)
	}
}

// ST37：两个模型分别记账——快速模型的 token 不能混进主模型账本，
// 否则成本看板按不同单价算总账会得出假数。
func TestUsageSplitV124(t *testing.T) {
	e := &Engine{Model: "main-big"}
	e.SetRouter(true, "fast-small")

	// 主模型一轮：100 输入 / 30 命中 / 20 输出。
	e.RecordModelUsageForTest("main-big", 100, 30, 20)
	// 快速模型两轮。
	e.RecordModelUsageForTest("fast-small", 50, 0, 10)
	e.RecordModelUsageForTest("fast-small", 25, 0, 5)

	if e.Turns() != 1 {
		t.Fatalf("主模型轮数 = %d, 期望 1", e.Turns())
	}
	in, out, turns := e.fastUsage()
	if turns != 2 {
		t.Fatalf("快速模型轮数 = %d, 期望 2", turns)
	}
	if in != 75 || out != 15 {
		t.Fatalf("快速模型用量 = in %d / out %d, 期望 75/15", in, out)
	}

	// 主模型账本必须只含自己那一笔：输入 100 + 输出 20，不能被 fast 的 75 污染。
	totalIn, totalOut, _ := e.Usage()
	if totalIn != 100 || totalOut != 20 {
		t.Fatalf("主模型账本 = in %d / out %d, 期望 100/20（快速模型不应混入）", totalIn, totalOut)
	}
}

// ST38：子代理预算——单次任务回灌的工具输出必须被截到上限，且截断要留可辨认的标记。
func TestSubAgentBudgetV124(t *testing.T) {
	long := strings.Repeat("日志行-填充内容\n", 4000) // 约 3.6 万字符
	got := ClipSubResultForTest(long, 2000)
	// 正文必须严格压到 2000 字以内，剩下的只是省略提示 banner（<200 字）。
	if n := len([]rune(got)); n > 2200 {
		t.Fatalf("截断后长度 = %d 字符，正文未被压到预算内", n)
	}
	if !strings.Contains(got, "截断") && !strings.Contains(got, "省略") {
		t.Fatalf("截断后应带可见标记，实际尾部: %q", tail(got, 60))
	}
	// 首尾都要留：报错在头部、结论在尾部，只留头会丢关键信息。
	if !strings.HasPrefix(got, "日志行") {
		t.Fatalf("截断后应保留开头，实际: %q", head(got, 40))
	}

	short := "编译通过"
	if got := ClipSubResultForTest(short, 2000); got != short {
		t.Fatalf("未超限内容不应被改写，实际 %q", got)
	}
	if got := ClipSubResultForTest("", 2000); got != "" {
		t.Fatalf("空输入应返回空，实际 %q", got)
	}
	// 上限为 0 或负数时不能把内容全吃掉。
	if got := ClipSubResultForTest(short, 0); got == "" {
		t.Fatalf("上限为 0 时应回退默认值而不是清空")
	}
}

func head(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
