package selftest

// v1.25 新增用例：目标收敛循环、Git 提交回滚闭环、并行写子代理隔离合并、
// 覆盖率与性能基线、结构化数据查询、图片理解与任务侧栏。
//
// 取向与 v1.24 一致：造真实数据走主路径，断言可观测结果；不依赖网络与模型。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"win7harness/internal/agent"
	"win7harness/internal/core"
)

// caseGoalLoop 目标收敛：验证"执行→整体验证失败→再来一轮→达标"，
// 以及连续无进展时及时中止（不能无限空转）。
func caseGoalLoop(e *env) (Status, string, string) {
	steps := []string{"改 A 文件", "改 B 文件", "跑测试"}
	execFail := 0
	verifyFails := 1
	r := &agent.GoalRunner{
		Goal: "让构建通过",
		Plan: func(goal string) ([]string, error) { return steps, nil },
		Exec: func(step string, round int) (string, error) {
			if round == 1 && step == "改 B 文件" {
				execFail++
				return "", fmt.Errorf("模拟失败")
			}
			return "已完成：" + step, nil
		},
		Verify: func(round int) (bool, string) {
			if round <= verifyFails {
				return false, "测试未通过：1 个失败"
			}
			return true, "构建与测试全部通过"
		},
		MaxRounds: 5,
	}
	rep := r.Run()
	if !rep.Achieved() {
		return StatusFail, "未达成（停止原因 " + rep.Stop + "）：" + rep.Summary(),
			"检查 GoalRunner 的验证回炉逻辑"
	}
	if rep.Rounds != 2 {
		return StatusFail, fmt.Sprintf("用掉 %d 轮（期望 2 轮：失败一轮 + 成功一轮）", rep.Rounds),
			"轮数统计或验证失败后的重试逻辑不对"
	}
	if execFail != 1 {
		return StatusFail, "首轮失败步骤没有按预期失败次数执行",
			"单步失败应留给下一轮重试"
	}
	if rep.Progress() != fmt.Sprintf("%d/%d", len(steps), len(steps)) {
		return StatusFail, "达成时完成度不是全满：" + rep.Progress(),
			"达成后应把全部步骤标记完成"
	}

	// 无进展必须中止，不能空转
	stall := &agent.GoalRunner{
		Goal:      "不可能完成",
		Plan:      func(goal string) ([]string, error) { return []string{"做点无用功"}, nil },
		Exec:      func(step string, round int) (string, error) { return "", fmt.Errorf("一直失败") },
		Verify:    func(round int) (bool, string) { return false, "还是不行" },
		MaxRounds: 10, StallLimit: 2,
	}
	rep2 := stall.Run()
	if rep2.Stop != "stalled" {
		return StatusFail, "无进展时停止原因是 " + rep2.Stop + "（期望 stalled）",
			"连续无进展应触发停滞保护"
	}
	if rep2.Rounds != 2 {
		return StatusFail, fmt.Sprintf("停滞保护用掉 %d 轮（期望 2）", rep2.Rounds),
			"StallLimit 未生效"
	}

	// 拆不出步骤要明确失败
	empty := &agent.GoalRunner{
		Goal: "空目标",
		Plan: func(goal string) ([]string, error) { return nil, nil },
	}
	if rep3 := empty.Run(); rep3.Stop != "no-plan" {
		return StatusFail, "空计划时停止原因是 " + rep3.Stop, "应返回 no-plan"
	}
	return StatusPass, fmt.Sprintf("2 轮收敛达成 3 步；无进展 2 轮中止；空计划拒绝执行"), ""
}

// caseGitFlow git 提交与回滚：真建仓库、真提交、真回滚。
func caseGitFlow(e *env) (Status, string, string) {
	if _, err := exec.LookPath("git"); err != nil {
		return StatusSkip, "环境未安装 git，跳过", ""
	}
	dir := e.path("selftest-git")
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StatusFail, "建目录失败: " + err.Error(), "检查工作区写权限"
	}
	defer os.RemoveAll(dir)
	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if _, err := run("init"); err != nil {
		return StatusFail, "git init 失败: " + err.Error(), "检查 git 是否可用"
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v1\n"), 0o644); err != nil {
		return StatusFail, "写文件失败: " + err.Error(), "检查工作区写权限"
	}
	if !core.IsGitRepo(dir) {
		return StatusFail, "IsGitRepo 未识别新建仓库", "检查 gitflow.go 的仓库判定"
	}
	msg, err := core.CommitAll(dir, "")
	if err != nil {
		return StatusFail, "首次提交失败: " + err.Error(), "检查 CommitAll 的预检与 add/commit 链路"
	}
	if !strings.Contains(msg, "提交") {
		return StatusFail, "提交结果缺少说明：" + msg, "CommitAll 应返回可读结果"
	}
	if changes := core.GitChanges(dir); len(changes) != 0 {
		return StatusFail, fmt.Sprintf("提交后仍有 %d 处改动", len(changes)), "add 未覆盖全部改动"
	}

	// 再改一次并提交，然后回滚到上一个提交
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v2\n"), 0o644); err != nil {
		return StatusFail, "写文件失败: " + err.Error(), "检查工作区写权限"
	}
	if _, err := core.CommitAll(dir, "第二次改动"); err != nil {
		return StatusFail, "第二次提交失败: " + err.Error(), "检查 CommitAll"
	}
	if _, err := core.GitRewind(dir, 1); err != nil {
		return StatusFail, "回滚失败: " + err.Error(), "检查 GitRewind"
	}
	data, err := os.ReadFile(filepath.Join(dir, "a.txt"))
	if err != nil {
		return StatusFail, "回滚后文件丢失: " + err.Error(), "回滚应保留文件内容"
	}
	if strings.TrimSpace(string(data)) != "v1" {
		return StatusFail, "回滚后内容为 " + strings.TrimSpace(string(data)) + "（期望 v1）",
			"检查 GitRewind 的回退深度"
	}
	// 空仓库预检应给出可读原因
	empty := e.path("selftest-git-empty")
	os.RemoveAll(empty)
	os.MkdirAll(empty, 0o755)
	defer os.RemoveAll(empty)
	if _, err := core.CommitAll(empty, "x"); err == nil {
		return StatusFail, "非 git 目录竟然提交成功", "CommitAll 应先校验仓库存在"
	}
	return StatusPass, "提交、二次提交、回滚 1 步内容还原正确；非仓库被拦下", ""
}

// caseParallelMerge 并行写子代理：隔离副本改动合并、同文件冲突另存。
func caseParallelMerge(e *env) (Status, string, string) {
	root := e.path("selftest-merge")
	os.RemoveAll(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return StatusFail, "建目录失败: " + err.Error(), "检查工作区写权限"
	}
	defer os.RemoveAll(root)
	if err := os.WriteFile(filepath.Join(root, "common.txt"), []byte("base\n"), 0o644); err != nil {
		return StatusFail, "写文件失败: " + err.Error(), "检查工作区写权限"
	}
	rep, err := agent.MergeFixture(root, []agent.MergeFixtureRun{
		{Desc: "子任务A", Changed: map[string]string{
			"a.txt":      "A 的产出\n",
			"common.txt": "A 改的\n",
		}},
		{Desc: "子任务B", Changed: map[string]string{
			"b.txt":      "B 的产出\n",
			"common.txt": "B 改的\n",
		}},
	})
	if err != nil {
		return StatusFail, "合并失败: " + err.Error(), "检查 MergeWrites 的写入路径"
	}
	if len(rep.Merged) != 3 {
		return StatusFail, fmt.Sprintf("合入 %d 个文件（期望 3：a.txt/b.txt/common.txt 首个胜出）", len(rep.Merged)),
			"独立改动应全部合入，同文件只合入第一个"
	}
	if len(rep.Conflicts) != 1 {
		return StatusFail, fmt.Sprintf("冲突记录 %d 条（期望 1）", len(rep.Conflicts)),
			"同文件被两个子任务改动应记为冲突"
	}
	if b, err := os.ReadFile(filepath.Join(root, "common.txt")); err != nil || strings.TrimSpace(string(b)) != "A 改的" {
		return StatusFail, "冲突文件未按第一个子任务胜出回写", "检查 MergeWrites 的 owner 逻辑"
	}
	entries, _ := os.ReadDir(root)
	alt := 0
	for _, it := range entries {
		if strings.Contains(it.Name(), ".from-") {
			alt++
		}
	}
	if alt != 1 {
		return StatusFail, fmt.Sprintf("冲突另存副本 %d 个（期望 1）", alt), "冲突内容应以 .from-<描述> 另存"
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err != nil {
		return StatusFail, "独立改动 a.txt 未合入", "检查 copyFile 写入"
	}
	return StatusPass, "3 个改动合入、1 处冲突另存、首个胜出规则正确", ""
}

// caseBaseline 覆盖率与性能基线：解析真实 coverprofile 与 benchmark 输出，并比对基线。
func caseBaseline(e *env) (Status, string, string) {
	profile := "mode: set\n" +
		"win7harness/internal/core/a.go:10.20,12.30 2 1\n" +
		"win7harness/internal/core/a.go:20.10,22.20 3 0\n" +
		"win7harness/internal/core/b.go:5.10,7.20 4 1\n"
	all, pkgs := core.ParseCoverProfile(profile)
	if all.Total == 0 {
		return StatusFail, "覆盖率统计为 0", "检查 ParseCoverProfile 的语句计数解析"
	}
	if all.Covered != 6 || all.Total != 9 {
		return StatusFail, fmt.Sprintf("覆盖率统计 %d/%d（期望 6/9）", all.Covered, all.Total),
			"语句数应按块计数累加"
	}
	if len(pkgs) != 1 {
		return StatusFail, fmt.Sprintf("按包统计 %d 个（期望 1）", len(pkgs)), "包聚合逻辑不对"
	}
	txt := core.FormatCoverage(all, pkgs, 70)
	if !strings.Contains(txt, "%") {
		return StatusFail, "覆盖率报告缺少百分比：" + txt, "检查 FormatCoverage"
	}

	bench := "BenchmarkParse-8    \t 1000\t  1234 ns/op\nBenchmarkIndex-8\t 500\t 5678 ns/op\n"
	cur := core.ParseBenchOutput(bench)
	if len(cur) != 2 {
		return StatusFail, fmt.Sprintf("解析出 %d 个基准（期望 2）", len(cur)), "检查 ParseBenchOutput 的行匹配"
	}
	base := map[string]core.BenchCase{}
	for _, c := range cur {
		base[c.Name] = core.BenchCase{Name: c.Name, NsPerOp: c.NsPerOp * 2} // 基线更慢：应判定为提速
	}
	deltas := core.CompareBench(base, cur)
	if len(deltas) != 2 {
		return StatusFail, fmt.Sprintf("比对出 %d 项（期望 2）", len(deltas)), "检查 CompareBench"
	}
	for _, d := range deltas {
		if d.Pct >= 0 || d.Status != "improve" {
			return StatusFail, fmt.Sprintf("%s 提速却报 %.1f%%（%s）", d.Name, d.Pct, d.Status),
				"提速应判定为 improve 且变化率为负"
		}
	}
	if s := core.FormatBenchDeltas(deltas); !strings.Contains(s, "%") {
		return StatusFail, "基线对比文本缺少变化率：" + s, "检查 FormatBenchDeltas"
	}
	return StatusPass, "覆盖率 6/9 语句、按包聚合正常；2 个基准解析并识别出提速", ""
}

// caseDataTable 结构化数据查询：真造 CSV，用工具做过滤、求和、分组。
func caseDataTable(e *env) (Status, string, string) {
	p := e.path("orders.csv")
	csv := "region,amount,channel\n华东,120,app\n华东,80,web\n华南,200,app\n华北,50,web\n"
	if err := os.WriteFile(p, []byte(csv), 0o644); err != nil {
		return StatusFail, "写 CSV 失败: " + err.Error(), "检查工作区写权限"
	}
	defer os.Remove(p)

	res, err := e.call("datatable", map[string]interface{}{"path": p, "op": "info"})
	if err != nil {
		return StatusFail, "读取表结构失败: " + err.Error(), "检查 CSV 解析"
	}
	if !strings.Contains(res.Output, "region") || !strings.Contains(res.Output, "4") {
		return StatusFail, "表结构输出不含列名或行数：" + res.Output, "检查 LoadTable 与 info 渲染"
	}
	res, err = e.call("datatable", map[string]interface{}{
		"path": p, "op": "filter", "column": "region", "operator": "=", "value": "华东",
	})
	if err != nil {
		return StatusFail, "过滤失败: " + err.Error(), "检查 filter 分支"
	}
	if strings.Contains(res.Output, "华南") || !strings.Contains(res.Output, "华东") {
		return StatusFail, "过滤结果不正确：" + res.Output, "检查比较符与列值匹配"
	}
	res, err = e.call("datatable", map[string]interface{}{"path": p, "op": "sum", "column": "amount"})
	if err != nil {
		return StatusFail, "求和失败: " + err.Error(), "检查 sum 分支的数字解析"
	}
	if !strings.Contains(res.Output, "450") {
		return StatusFail, "求和结果不是 450：" + res.Output, "sum 应累加 amount 列"
	}
	res, err = e.call("datatable", map[string]interface{}{"path": p, "op": "group", "column": "region"})
	if err != nil {
		return StatusFail, "分组失败: " + err.Error(), "检查 group 分支"
	}
	if !strings.Contains(res.Output, "华东") || !strings.Contains(res.Output, "2") {
		return StatusFail, "分组结果缺华东计数：" + res.Output, "group 应输出每组的行数"
	}
	return StatusPass, "info/filter/sum/group 四个操作在真实 CSV 上结果正确", ""
}

// caseImageAndSidebar 图片理解与任务侧栏：read_image 产出可发送的图片载荷，
// 引擎把它转成多模态消息；侧栏能反映目标、待办与子任务状态。
func caseImageAndSidebar(e *env) (Status, string, string) {
	// 1x1 PNG
	png := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89}
	p := e.path("shot.png")
	if err := os.WriteFile(p, png, 0o644); err != nil {
		return StatusFail, "写图片失败: " + err.Error(), "检查工作区写权限"
	}
	defer os.Remove(p)
	res, err := e.call("read_image", map[string]interface{}{"path": p})
	if err != nil {
		return StatusFail, "read_image 失败: " + err.Error(), "检查工具注册与文件读取"
	}
	if !strings.Contains(res.Output, "1×1") {
		return StatusFail, "未解析出图片尺寸：" + res.Output, "检查 HTTP 无依赖的 PNG 头解析"
	}
	if !strings.Contains(res.Output, "data:image/png;base64,") {
		return StatusFail, "缺少图片载荷标记", "read_image 应回传 data URL 供多模态发送"
	}
	// 非图片文件要明确拒绝
	bad := e.path("not-image.txt")
	os.WriteFile(bad, []byte("x"), 0o644)
	defer os.Remove(bad)
	if _, err := e.call("read_image", map[string]interface{}{"path": bad}); err == nil {
		return StatusFail, "非图片文件竟然读取成功", "应按扩展名拒绝并给出支持的格式"
	}

	// 侧栏：待办 + 目标 + 子任务计数
	board := agent.NewTaskBoard()
	board.SetTodos([]agent.TodoItem{
		{Text: "改 A", Status: "done"},
		{Text: "改 B", Status: "doing"},
		{Text: "跑测试", Status: "pending"},
	})
	board.SubStart()
	board.SubStart()
	board.SubDone()
	board.SetGoalRunning("让构建通过")
	snap := board.Snapshot()
	if snap.Empty() {
		return StatusFail, "看板有内容却报告为空", "检查 Snapshot 与 Empty"
	}
	if snap.DoneCount != 1 || snap.AllCount != 3 {
		return StatusFail, fmt.Sprintf("待办统计 %d/%d（期望 1/3）", snap.DoneCount, snap.AllCount),
			"检查待办状态统计"
	}
	if snap.Running != 1 {
		return StatusFail, fmt.Sprintf("子任务计数 %d（期望 1）", snap.Running), "检查 SubStart/SubDone 配对"
	}
	if snap.GoalRun == "" {
		return StatusFail, "进行中的目标未进入看板", "检查 SetGoalRunning"
	}
	rep := &agent.GoalReport{Goal: "让构建通过", Stop: "achieved",
		Steps: []agent.GoalStep{{Desc: "改 A", Done: true}, {Desc: "跑测试", Done: true}}}
	board.SetGoal(rep)
	snap = board.Snapshot()
	if snap.Goal == nil || !snap.Goal.Achieved() || snap.GoalRun != "" {
		return StatusFail, "目标结束后看板状态未更新", "SetGoal 应清掉进行中标记并记录结果"
	}
	if snap.Goal.Progress() != "2/2" {
		return StatusFail, "目标进度 " + snap.Goal.Progress(), "检查 Progress 计算"
	}
	return StatusPass, "图片读取返回尺寸与可发送载荷、非图片被拒；侧栏待办/子任务/目标状态正确", ""
}
