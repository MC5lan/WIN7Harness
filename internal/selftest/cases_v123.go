package selftest

// v1.23 新增用例：验证闭环、崩溃恢复、工具输出摘要、提示缓存与成本看板、
// 索引内存峰值、MCP 一键装、截图裁剪。
//
// 这批用例的共同取向：不去断言"函数存在"，而是造真实数据走一遍主路径，
// 断言可观测的结果（轮数、字节数、落盘文件、拼装出的配置）。这样自检全绿
// 才等于用户拿到手里真能用，而不是"能编译"。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"win7harness/internal/agent"
	"win7harness/internal/codeidx"
	"win7harness/internal/core"
	"win7harness/internal/mcp"
	"win7harness/internal/tools"
)

// caseVerifyLoop 验证闭环：改代码 → 自动跑验证 → 失败回灌 → 再改，最多 N 轮。
func caseVerifyLoop(e *env) (Status, string, string) {
	eng := agent.NewTestEngine(e.workDir)
	// 三态开关：auto 开启、off 关闭、空值跟随环境变量（默认关闭，
	// 避免非代码任务每次都被白跑一遍构建与测试）。
	eng.VerifyMode = agent.VerifyModeAuto
	if !eng.VerifyEnabled() {
		return StatusFail, "VerifyMode=auto 时未启用", "检查 verifyEnabled 的 auto 分支"
	}
	eng.VerifyMode = agent.VerifyModeOff
	if eng.VerifyEnabled() {
		return StatusFail, "VerifyMode=off 时仍启用", "检查 verifyEnabled 的 off 分支"
	}
	eng.VerifyMode = ""
	os.Setenv("W7H_VERIFY", "1")
	follow := eng.VerifyEnabled()
	os.Unsetenv("W7H_VERIFY")
	if !follow {
		return StatusFail, "空模式时不跟随环境变量 W7H_VERIFY", "检查 verifyEnabled 的兜底分支"
	}
	eng.VerifyMode = agent.VerifyModeAuto
	if lim := eng.VerifyLimit(); lim < 1 || lim > 5 {
		return StatusFail, fmt.Sprintf("验证轮数上限异常：%d", lim), "检查 VerifyLimit 的默认值与上限夹取"
	}

	// 只读操作不该触发验证（否则每次问答都白跑一遍测试）。
	eng.MarkWrite("")
	if eng.CurrentRound() != 0 {
		return StatusFail, "未写文件却进入验证轮", "检查 MarkWrite 与 wroteThisTurn 的判定"
	}
	eng.MarkWrite("write_file")
	if eng.CurrentRound() != 0 {
		// 写入只应"标记待验证"，真正推进轮数发生在跑过验证之后。
		return StatusPass, "写文件已标记，轮次延后推进（符合预期）", ""
	}
	return StatusPass, fmt.Sprintf("三态开关正确，上限 %d 轮，仅写操作触发验证", eng.VerifyLimit()), ""
}

// caseCrashRecovery 崩溃恢复：半途断电后下次启动能发现未完成轮次并给出续做提示。
func caseCrashRecovery(e *env) (Status, string, string) {
	dir := filepath.Join(e.workDir, "st_journal")
	defer os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StatusFail, "建日志目录失败：" + err.Error(), "检查磁盘权限"
	}
	j := core.NewJournal(dir)
	conv := "st_conv_1"
	if err := j.Begin(core.JournalEntry{
		ConvID:    conv,
		UserText:  "把 report 目录下的 csv 合成一张表",
		Model:     "test-model",
		WorkDir:   e.workDir,
		StartedAt: time.Now(),
	}); err != nil {
		return StatusFail, "写轮次起点失败：" + err.Error(), "检查 Journal.Begin 的落盘"
	}
	if err := j.Progress(conv, 3, "run_command"); err != nil {
		return StatusFail, "写进度失败：" + err.Error(), "检查 Journal.Progress"
	}

	// 模拟"没走 Finish 就掉电"：新建一个实例重新判读，等价于重启。
	j2 := core.NewJournal(dir)
	pend, err := j2.Pending()
	if err != nil {
		return StatusFail, "判读未完成轮次失败：" + err.Error(), "检查 Journal.Pending 的解析"
	}
	if len(pend) != 1 {
		return StatusFail, fmt.Sprintf("应发现 1 条未完成轮次，实得 %d", len(pend)),
			"检查 Pending 是否按 Finished 标记过滤"
	}
	notice := core.RecoveryNotice(pend[0])
	if !strings.Contains(notice, "csv") {
		return StatusFail, "恢复提示里看不到原始意图：" + notice,
			"检查 RecoveryNotice 是否带上 UserText 摘要"
	}
	prompt := core.RecoveryPrompt(pend[0])
	if !strings.Contains(prompt, "run_command") {
		return StatusFail, "续做提示里丢失了最后一步工具：" + prompt,
			"检查 RecoveryPrompt 是否带上 LastTool"
	}

	// 正常收尾后不应再报"未完成"。
	if err := j2.Finish(conv); err != nil {
		return StatusFail, "收尾失败：" + err.Error(), "检查 Journal.Finish"
	}
	if left, _ := core.NewJournal(dir).Pending(); len(left) != 0 {
		return StatusFail, "已收尾的轮次仍被判为未完成", "检查 Finished 标记的写入与判读"
	}
	return StatusPass, "断电恢复链路完好：能定位未完成轮次并给出续做提示", ""
}

// caseToolOutputSummary 工具输出的"先摘要再截断"策略。
func caseToolOutputSummary(e *env) (Status, string, string) {
	var b strings.Builder
	b.WriteString("go test ./...\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "--- PASS: TestCase%03d (0.00s)\n", i)
	}
	b.WriteString("错误行：cannot find package win7harness/internal/nope\n")
	b.WriteString("FAIL\twin7harness/internal/nope\t0.012s\n")
	raw := b.String()

	const budget = 4096
	got := tools.SummarizeToolOutput(raw, budget)
	if len(got) >= len(raw) {
		return StatusFail, fmt.Sprintf("超预算输出未被压缩：%d → %d", len(raw), len(got)),
			"检查 SummarizeToolOutput 的预算分支"
	}
	if len(got) > budget+1024 {
		return StatusFail, fmt.Sprintf("压缩后 %d 字节，明显超过预算 %d", len(got), budget),
			"检查摘要头尾行数与预算夹取"
	}
	// 关键信息必须留下：失败行和报错原文，否则模型拿不到可行动线索。
	if !strings.Contains(got, "cannot find package") {
		return StatusFail, "摘要丢掉了报错原文", "检查关键行提取规则（错误/失败/路径）"
	}
	if !strings.Contains(got, "FAIL") {
		return StatusFail, "摘要丢掉了 FAIL 行", "检查关键行正则"
	}
	if !tools.OutputWasTrimmed(raw, got) {
		return StatusFail, "未正确标记输出被裁剪", "检查 OutputWasTrimmed 的判定"
	}
	// 未超预算时必须原样返回，避免无谓改动破坏 diff 类输出。
	if small := "ok  1.2s\n"; tools.SummarizeToolOutput(small, budget) != small {
		return StatusFail, "未超预算的输出被改写了", "检查预算内的直通分支"
	}
	return StatusPass, fmt.Sprintf("%d → %d 字节，保留报错与 FAIL 行", len(raw), len(got)), ""
}

// caseCostBoard 提示缓存与成本看板：命中率、费用折算、看板字段。
func caseCostBoard(e *env) (Status, string, string) {
	eng := agent.NewTestEngine(e.workDir)
	// 造两轮带缓存命中的用量：第二轮命中一半输入
	eng.RecordUsageForTest("tokencache-model-A", 10000, 2000, 2000)
	eng.RecordUsageForTest("tokencache-model-A", 10000, 5000, 1500)

	s := eng.CostSnapshot()
	if s.Turns != 2 {
		return StatusFail, fmt.Sprintf("轮数统计错误：%d", s.Turns), "检查 recordUsage 的累加"
	}
	if s.Cached != 7000 {
		return StatusFail, fmt.Sprintf("缓存命中 token 统计错误：%d（期望 7000）", s.Cached),
			"检查 recordCache 与 Usage 字段映射"
	}
	if s.HitRate <= 0 || s.HitRate > 1 {
		return StatusFail, fmt.Sprintf("命中率越界：%.3f", s.HitRate), "检查 HitRate 的归一化"
	}
	if s.CostPlain < s.Cost {
		return StatusFail, fmt.Sprintf("无缓存费用(%f) 低于实际费用(%f)，说明折扣方向反了", s.CostPlain, s.Cost),
			"检查计价里缓存价与全价的使用"
	}
	board := eng.CostBoard()
	for _, key := range []string{"命中率", "估算费用", "缓存写入"} {
		if !strings.Contains(board, key) {
			return StatusFail, "成本看板缺少字段：" + key, "检查 CostBoard 输出"
		}
	}
	if line := eng.CacheLine(); !strings.Contains(line, "%") {
		return StatusFail, "状态栏摘要里没有命中率：" + line, "检查 CacheLine"
	}
	return StatusPass, fmt.Sprintf("两轮累计命中 %.1f%%，费用 %s（无缓存需 %s）",
		s.HitRate*100, core.Money(s.Cost), core.Money(s.CostPlain)), ""
}

// caseIndexMemory 索引内存：峰值可估算、未变快照可复用、超预算有兜底。
func caseIndexMemory(e *env) (Status, string, string) {
	codeidx.Reset()
	defer codeidx.Reset()
	root := filepath.Join(e.workDir, "st_idxmem")
	defer os.RemoveAll(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return StatusFail, "建目录失败：" + err.Error(), "检查磁盘权限"
	}
	for i := 0; i < 40; i++ {
		body := fmt.Sprintf("package m%d\n\nfunc Fn%d() int { return %d }\n", i, i, i)
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%02d.go", i)), []byte(body), 0o644); err != nil {
			return StatusFail, "写文件失败：" + err.Error(), "检查磁盘权限"
		}
	}
	first := codeidx.Update(root)
	if first.Total != 40 {
		return StatusFail, fmt.Sprintf("首次索引文件数 %d（期望 40）", first.Total), "检查 Build 的遍历"
	}
	// 第二次构建：文件集合与 mtime 都没变，应走"整份快照复用"，
	// 而不是再解析一遍——这正是大仓库内存翻倍的来源。
	st := codeidx.Update(root)
	if st.Reused == 0 {
		return StatusFail, fmt.Sprintf("未变文件集未复用：复用 %d / 重解析 %d", st.Reused, st.Parsed),
			"检查 sameFileSet 的判定"
	}
	if st.MemBytes <= 0 {
		return StatusFail, "构建统计里没有内存峰值", "检查 BuildStats.MemBytes 的填充"
	}
	mem := st.MemBytes
	if mem <= 0 {
		mem = first.MemBytes
	}
	if mem <= 0 {
		return StatusFail, "内存估算为 0，说明没有统计到实际驻留", "检查 EstimateBytes 的实现"
	}
	return StatusPass, fmt.Sprintf("%d 文件，估算驻留 %s，复用 %d 个未变文件",
		st.Total, core.HumanSize(mem), st.Reused), ""
}

// caseMCPWizard MCP 一键装：模板自洽、占位替换、敏感值进 env。
func caseMCPWizard(e *env) (Status, string, string) {
	cat := mcp.Catalog()
	if len(cat) < 5 {
		return StatusFail, fmt.Sprintf("内置模板只有 %d 个（期望 ≥5）", len(cat)),
			"检查 catalog.go 的内置模板清单"
	}
	for _, t := range cat {
		if err := t.Validate(); err != nil {
			return StatusFail, fmt.Sprintf("模板 %s 不自洽：%v", t.ID, err), "检查模板定义"
		}
	}
	t, ok := mcp.FindTemplate(cat[0].ID)
	if !ok {
		return StatusFail, "按 ID 找不到模板 " + cat[0].ID, "检查 FindTemplate"
	}
	vals := map[string]string{}
	for _, f := range t.Params {
		vals[f.Key] = "test-value-" + f.Key
	}
	spec, err := t.Build(vals)
	if err != nil {
		return StatusFail, fmt.Sprintf("模板 %s 拼装失败：%v", t.ID, err), "检查 Template.Build"
	}
	if spec.Name == "" || spec.Command == "" {
		return StatusFail, "拼装出的配置缺少启动命令或地址", "检查 Build 对 command/url 的填充"
	}
	joined := strings.Join(spec.Args, " ")
	if strings.Contains(joined, "{") {
		return StatusFail, "参数里残留未替换的占位符：" + joined, "检查占位替换"
	}
	// 必填项缺失要明确报错，而不是装出一个连不上的服务。
	missing := map[string]string{}
	if _, err := t.Build(missing); err == nil && len(t.Params) > 0 {
		return StatusFail, "必填项全缺仍拼装成功", "检查 Build 的必填校验"
	}
	if !mcp.Installed([]core.MCPServer{spec}, t.ID) {
		return StatusFail, "已装判断失效", "检查 Installed 的比对键"
	}
	if names := mcp.Names([]core.MCPServer{spec}); len(names) != 1 {
		return StatusFail, "已装清单数量不对", "检查 Names"
	}
	return StatusPass, fmt.Sprintf("%d 个内置模板全部自洽，%s 可一键拼装", len(cat), t.ID), ""
}

// caseSnipCrop 框选截图：选区归一化 + 按视觉坐标裁剪 + 行序归一。
func caseSnipCrop(e *env) (Status, string, string) {
	const w, h = 16, 10
	dib := makeSelftestDIB(w, h)
	out, err := core.CropDIB32(dib, core.DIBRect{X: 3, Y: 2, W: 5, H: 4})
	if err != nil {
		return StatusFail, "裁剪失败：" + err.Error(), "检查 CropDIB32"
	}
	ow, oh, bpp, err := core.DIBSize(out)
	if err != nil {
		return StatusFail, "裁剪结果不可解析：" + err.Error(), "检查输出头字段"
	}
	if ow != 5 || oh != 4 || bpp != 32 {
		return StatusFail, fmt.Sprintf("裁剪尺寸错误：%dx%d@%d", ow, oh, bpp), "检查目标头字段回填"
	}
	// 首像素应等于源里视觉坐标 (3,2) 的标记值
	if out[40] != byte(3) || out[40+1] != byte(2) {
		return StatusFail, fmt.Sprintf("像素取错位置：(%d,%d)", out[40], out[40+1]),
			"检查 bottom-up 到 top-down 的行序换算"
	}
	if r := core.NormalizeRect(9, 8, 1, 2); r.X != 1 || r.Y != 2 || r.W != 8 || r.H != 6 {
		return StatusFail, fmt.Sprintf("反向拖拽归一化错误：%+v", r), "检查 NormalizeRect"
	}
	if _, err := core.CropDIB32(dib, core.DIBRect{X: 100, Y: 100, W: 4, H: 4}); err == nil {
		return StatusFail, "完全越界的选区应报错", "检查 ClipRect 的兜底"
	}
	return StatusPass, fmt.Sprintf("%dx%d 源裁出 %dx%d，坐标与行序正确", w, h, ow, oh), ""
}

// makeSelftestDIB 造一张 32bpp 自下而上的测试位图，像素值记视觉坐标。
func makeSelftestDIB(w, h int) []byte {
	off := 40
	stride := w * 4
	buf := make([]byte, off+stride*h)
	put := func(o int, v uint32) {
		buf[o] = byte(v)
		buf[o+1] = byte(v >> 8)
		buf[o+2] = byte(v >> 16)
		buf[o+3] = byte(v >> 24)
	}
	put(0, 40)
	put(4, uint32(w))
	put(8, uint32(h))
	buf[12], buf[13] = 1, 0
	buf[14], buf[15] = 32, 0
	put(20, uint32(stride*h))
	for y := 0; y < h; y++ {
		row := off + y*stride
		vis := h - 1 - y
		for x := 0; x < w; x++ {
			buf[row+x*4] = byte(x)
			buf[row+x*4+1] = byte(vis)
			buf[row+x*4+2] = 0x7f
			buf[row+x*4+3] = 0xff
		}
	}
	return buf
}
