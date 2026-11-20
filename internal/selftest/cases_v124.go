package selftest

// v1.24 新增用例：联网搜索解析、跨会话检索、诊断结构化、子代理预算、
// 模型路由分账、工具卡折叠分组。
//
// 取向与 v1.23 一致：造真实数据走主路径，断言可观测结果。
// 网络相关的用例只验解析与降级，真发请求交给 ST16 与用户实际使用。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"win7harness/internal/agent"
	"win7harness/internal/coreutil"
	"win7harness/internal/tools"
)

// caseWebSearchParse 联网搜索：结果页解析要能剥跳转壳、抓摘要、按上限截断，
// 并且无结果页必须返回空而不是编造。
func caseWebSearchParse(e *env) (Status, string, string) {
	page := `<div class="result"><h2 class="result__title">` +
		`<a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fgo.dev%2Fdoc%2F&amp;rut=x">Go 官方文档</a>` +
		`</h2><a class="result__snippet" href="#">Go 是开源编程语言，适合构建简单可靠的软件。</a></div>` +
		`<div class="result"><h2 class="result__title">` +
		`<a class="result__a" href="https://pkg.go.dev/net/http">net/http</a>` +
		`</h2><a class="result__snippet" href="#">HTTP 客户端与服务端实现。</a></div>`
	hits := tools.ParseSearchPageForTest("duckduckgo", page, 10)
	if len(hits) != 2 {
		return StatusFail, fmt.Sprintf("解析出 %d 条（期望 2）", len(hits)),
			"结果页结构与解析规则可能已变化，请检查 websearch.go 的正则"
	}
	if hits[0].URL != "https://go.dev/doc/" {
		return StatusFail, "跳转壳未被剥离：" + hits[0].URL,
			"检查 cleanDuckURL 对 uddg 参数的解码"
	}
	if !strings.Contains(hits[0].Snippet, "开源") {
		return StatusFail, "摘要未解析：" + hits[0].Snippet,
			"检查 result__snippet 规则"
	}
	if got := tools.ParseSearchPageForTest("duckduckgo", page, 1); len(got) != 1 {
		return StatusFail, fmt.Sprintf("limit=1 返回 %d 条", len(got)),
			"上限截断未生效"
	}
	for _, name := range tools.WebSearchEngines() {
		if got := tools.ParseSearchPageForTest(name, "<html>no results</html>", 5); len(got) != 0 {
			return StatusFail, name + " 在无结果页上伪造了结果",
				"解析器不应从无结果页提取条目"
		}
	}
	return StatusPass, fmt.Sprintf("%d 个引擎解析正常，跳转壳已剥离", len(tools.WebSearchEngines())), ""
}

// caseHistorySearch 跨会话检索：真落盘两个会话再查，验证多词 AND、时间窗与角色标注。
func caseHistorySearch(e *env) (Status, string, string) {
	dir := filepath.Join(e.workDir, "selftest-history")
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StatusFail, "建目录失败: " + err.Error(), "检查工作区写权限"
	}
	defer os.RemoveAll(dir)

	now := time.Now()
	write := func(name, id, title string, at time.Time, msgs string) error {
		body := fmt.Sprintf(`{"v":1,"conv":{"id":%q,"title":%q,"at":%q,"msgs":[%s]}}`,
			id, title, at.Format(time.RFC3339), msgs)
		return os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	if err := write("a.json", "a", "索引构建优化", now,
		fmt.Sprintf(`{"role":"user","text":"索引构建内存峰值太高","at":%q},`, now.Format(time.RFC3339))+
			fmt.Sprintf(`{"role":"assistant","text":"复用上次快照的峰值统计即可","at":%q}`, now.Format(time.RFC3339))); err != nil {
		return StatusFail, "写会话失败: " + err.Error(), "检查工作区写权限"
	}
	old := now.AddDate(0, 0, -40)
	if err := write("b.json", "b", "旧会话", old,
		fmt.Sprintf(`{"role":"user","text":"帮我写个 Python 脚本","at":%q}`, old.Format(time.RFC3339))); err != nil {
		return StatusFail, "写会话失败: " + err.Error(), "检查工作区写权限"
	}

	hits, scanned, err := tools.SearchHistory(dir, "内存峰值", 10, 0, now)
	if err != nil {
		return StatusFail, "检索报错: " + err.Error(), "检查 history.go 的文件遍历"
	}
	if scanned != 2 {
		return StatusFail, fmt.Sprintf("扫描了 %d 个会话（期望 2）", scanned), "检查会话文件识别规则"
	}
	if len(hits) != 1 {
		return StatusFail, fmt.Sprintf("命中 %d 条（期望 1）", len(hits)), "全文检索未命中落盘内容"
	}
	if hits[0].Title != "索引构建优化" || hits[0].Role != "user" {
		return StatusFail, fmt.Sprintf("命中信息异常：%s / %s", hits[0].Title, hits[0].Role),
			"检查标题与角色标注"
	}
	// 多词必须 AND：两个词分别在两条消息里时不算命中。
	if got, _, _ := tools.SearchHistory(dir, "内存 脚本", 10, 0, now); len(got) != 0 {
		return StatusFail, "多词检索退化成 OR", "检查 matchAll 是否要求同时命中"
	}
	// 时间窗按会话内容时间算：40 天前的会话不该出现在 7 天窗口里。
	if _, n7, _ := tools.SearchHistory(dir, "Python", 10, 7, now); n7 != 1 {
		return StatusFail, fmt.Sprintf("7 天窗口内扫描 %d 个会话（期望 1）", n7),
			"检查时间窗是否按会话内容时间而非文件 mtime 判定"
	}
	return StatusPass, "2 个会话可检索，多词 AND 与时间窗正确", ""
}

// caseDiagStructured 诊断结构化：Go / TypeScript / Python 三种输出都要抽得出定位。
func caseDiagStructured(e *env) (Status, string, string) {
	goOut := "# win7harness/internal/app\ninternal/app/chatview.go:332:8: t.Expanded undefined\nFAIL\twin7harness/internal/app [build failed]"
	ds := tools.ParseDiagnostics(goOut, "")
	if len(ds) == 0 {
		return StatusFail, "Go 编译错误未抽出诊断", "检查 reDiagGo 规则"
	}
	if ds[0].File != "internal/app/chatview.go" || ds[0].Line != 332 || ds[0].Col != 8 {
		return StatusFail, fmt.Sprintf("定位错误：%s:%d:%d", ds[0].File, ds[0].Line, ds[0].Col),
			"检查文件、行、列的分组解析"
	}
	tsOut := "src/a.ts(12,3): error TS2304: Cannot find name 'x'"
	if d := tools.ParseDiagnostics(tsOut, "tsc"); len(d) == 0 || d[0].Line != 12 || d[0].Col != 3 {
		return StatusFail, "TypeScript 诊断解析失败", "检查 reDiagTS 规则"
	}
	pyOut := "Traceback (most recent call last):\n  File \"app/main.py\", line 42, in <module>\n    run()\nValueError: bad value"
	if d := tools.ParseDiagnostics(pyOut, ""); len(d) == 0 || d[0].Line != 42 {
		return StatusFail, "Python 回溯未定位到行", "检查 reDiagPyFile 规则"
	}
	// 正常输出不能产出诊断，否则会把"测试全过"误报成失败。
	if d := tools.ParseDiagnostics("ok\nPASS\nall tests passed", ""); len(d) != 0 {
		return StatusFail, fmt.Sprintf("正常输出被误判为 %d 条错误", len(d)),
			"通用规则过于宽松，检查 reDiagGeneric"
	}
	block := tools.FormatDiagnostics(ds)
	if !strings.Contains(block, "chatview.go:332") {
		return StatusFail, "格式化后丢失 file:line", "检查 FormatDiagnostics"
	}
	return StatusPass, "Go / TS / Python 三态定位正确，正常输出不误报", ""
}

// caseSubAgentBudget 子代理预算：超长结论必须被压到上限内、留头留尾、带省略提示。
func caseSubAgentBudget(e *env) (Status, string, string) {
	long := strings.Repeat("步骤日志行内容\n", 5000)
	got := agent.ClipSubResultForTest(long, 3000)
	n := len([]rune(got))
	if n > 3300 {
		return StatusFail, fmt.Sprintf("裁剪后 %d 字（预算 3000）", n), "检查 clipSubResult 的分段比例"
	}
	if !strings.Contains(got, "省略") {
		return StatusFail, "裁剪后缺少省略提示", "裁剪必须让模型知道内容被截断"
	}
	if !strings.HasPrefix(got, "步骤日志行") {
		return StatusFail, "裁剪后丢了开头", "头部通常是要点，必须保留"
	}
	if s := agent.ClipSubResultForTest("短结论", 3000); s != "短结论" {
		return StatusFail, "未超限内容被改写：" + s, "预算内内容应原样返回"
	}
	// 预算配成 0 时必须回退默认值，不能把结论清空。
	if s := agent.ClipSubResultForTest("x", 0); s == "" {
		return StatusFail, "预算为 0 时内容被清空", "0 应视为使用默认预算"
	}
	return StatusPass, fmt.Sprintf("超长结论由 %d 字压到 %d 字，头尾均保留", len([]rune(long)), n), ""
}

// caseModelRouter 模型路由：开关、未配快速模型、同名三种情况都要回落到主模型，
// 且两个模型的用量必须分账——否则成本看板算出来是假数。
func caseModelRouter(e *env) (Status, string, string) {
	eng := agent.NewTestEngine(e.workDir)
	eng.Model = "main-big"

	eng.SetRouter(false, "fast-small")
	if got := eng.ModelFor(agent.JobCompact); got != "main-big" {
		return StatusFail, "路由关闭时压缩任务仍走了 " + got, "检查 ModelFor 的关闭分支"
	}
	eng.SetRouter(true, "")
	if got := eng.ModelFor(agent.JobCompact); got != "main-big" {
		return StatusFail, "未配快速模型时未回落主模型", "空快速模型必须回落"
	}
	if !strings.Contains(eng.RouterSummary(), "未指定快速模型") {
		return StatusFail, "摘要未提示未指定快速模型", "摘要必须与真实行为一致"
	}
	eng.SetRouter(true, "fast-small")
	if got := eng.ModelFor(agent.JobCompact); got != "fast-small" {
		return StatusFail, "压缩任务未路由到快速模型", "检查 JobCompact 分支"
	}
	if got := eng.ModelFor(agent.JobSubWrite); got != "main-big" {
		return StatusFail, "写文件子代理被降级到快速模型", "写操作必须用主模型"
	}

	eng.RecordModelUsageForTest("main-big", 100, 30, 20)
	eng.RecordModelUsageForTest("fast-small", 50, 0, 10)
	if in, out, turns := eng.Usage(); in != 100 || out != 20 || turns != 1 {
		return StatusFail, fmt.Sprintf("主账本被污染：in %d out %d turns %d", in, out, turns),
			"检查 addUsageFor 的分桶条件"
	}
	if _, _, ft := eng.SubUsage(); ft != 0 {
		// SubUsage 统计的是子代理，这里没跑子代理，应为 0。
		return StatusFail, "未跑子代理却记了子代理轮次", "检查 absorbSubUsage 调用点"
	}
	if _, _, fturns := eng.FastUsageForTest(); fturns != 1 {
		return StatusFail, "快速模型轮数未单独记账", "检查 fastTurns 累加"
	}
	return StatusPass, "路由四种状态正确，主/快速模型用量分账无污染", ""
}

// caseToolFold 工具卡折叠：连续 ≥3 条已结束才折，运行中的必逐条可见，展开后恢复。
func caseToolFold(e *env) (Status, string, string) {
	ok := coreutil.ToolSpan{Done: true, Elapsed: time.Second}
	bad := coreutil.ToolSpan{Done: true, Fail: true, Elapsed: 2 * time.Second}
	run := coreutil.ToolSpan{Done: false}

	if got := coreutil.FoldRuns([]coreutil.ToolSpan{ok, ok}, nil); len(got) != 0 {
		return StatusFail, "两条工具卡被折叠", "少于 3 条应逐条显示"
	}
	got := coreutil.FoldRuns([]coreutil.ToolSpan{ok, bad, ok}, nil)
	if len(got) != 1 || got[0].Len != 3 || got[0].Fails != 1 || got[0].Dur != 4*time.Second {
		return StatusFail, fmt.Sprintf("分组结果异常：%+v", got), "检查失败数与耗时累加"
	}
	if got := coreutil.FoldRuns([]coreutil.ToolSpan{ok, ok, run, ok, ok}, nil); len(got) != 0 {
		return StatusFail, "运行中的卡被发现折叠", "运行中的卡必须逐条可见"
	}
	got = coreutil.FoldRuns([]coreutil.ToolSpan{ok, ok, ok, run, ok, ok, ok}, nil)
	if len(got) != 2 || got[0].Start != 0 || got[1].Start != 4 {
		return StatusFail, fmt.Sprintf("两段分组异常：%+v", got), "分段应各自折叠"
	}
	if got := coreutil.FoldRuns([]coreutil.ToolSpan{ok, ok, ok}, map[int]bool{0: true}); len(got) != 0 {
		return StatusFail, "手动展开后仍被折叠", "展开状态应优先生效"
	}
	return StatusPass, "折叠边界、运行中拦截、展开回退均正确", ""
}
