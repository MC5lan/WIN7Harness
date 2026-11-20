package selftest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"win7harness/internal/agent"
	"win7harness/internal/codeidx"
	"win7harness/internal/core"
	"win7harness/internal/eval"
	"win7harness/internal/skills"
	"win7harness/internal/tools"
)

// allCases 返回全部用例。顺序即报告顺序，按"环境 → 文件 → 检索 → 执行 → 安全 → 状态 → 平台"递进，
// 越靠前的用例挂了，后面的基本没必要看。
func allCases() []testCase {
	return []testCase{
		{"ST01", "工具注册完整性", "基础", caseToolRegistry},
		{"ST02", "数据目录可写", "基础", caseDataDirWritable},
		{"ST03", "沙箱磁盘读写 1MB", "基础", caseDiskRoundTrip},
		{"ST04", "写入后读回（中文）", "文件", caseWriteRead},
		{"ST05", "精确替换唯一匹配", "文件", caseEditFile},
		{"ST06", "目录列举与通配符", "文件", caseListAndGlob},
		{"ST07", "内容检索命中", "检索", caseGrep},
		{"ST08", "符号检索命中", "检索", caseCodeSearch},
		{"ST09", "命令执行与输出捕获", "执行", caseRunCommand},
		{"ST10", "系统目录写保护", "安全", caseForbiddenPath},
		{"ST11", "危险命令分级", "安全", caseCommandRisk},
		{"ST12", "快照与还原", "状态", caseSnapshotRestore},
		{"ST13", "原子写与压缩往返", "状态", caseAtomicAndGzip},
		{"ST14", "中文与空格路径读写", "文件", caseUnicodePath},
		{"ST15", "渲染环境探测", "平台", casePlatformProbe},
		{"ST16", "模型连通性", "网络", caseProvider},
		{"ST17", "改动清单与多步回退", "状态", caseCheckpoints},
		{"ST18", "失败诊断分类", "诊断", caseDiagnose},
		{"ST19", "计划方案识别", "诊断", casePlanDetect},
		{"ST20", "技能发现与加载", "扩展", caseSkills},
		{"ST21", "索引增量与行号同步", "检索", caseIndexIncremental},
		{"ST22", "评测基线可执行", "评测", caseEvalBaseline},
		{"ST23", "测试结果解析与失败定位", "执行", caseRunTests},
		{"ST24", "网页正文提取与内网拦截", "网络", caseWebFetch},
		{"ST25", "低内存档位生效", "平台", caseLowMemory},
		{"ST26", "验证闭环自动串联", "执行", caseVerifyLoop},
		{"ST27", "崩溃与断电恢复", "状态", caseCrashRecovery},
		{"ST28", "工具输出先摘要再截断", "执行", caseToolOutputSummary},
		{"ST29", "提示缓存与成本看板", "成本", caseCostBoard},
		{"ST30", "索引内存峰值可估", "检索", caseIndexMemory},
		{"ST31", "MCP 一键装模板", "扩展", caseMCPWizard},
		{"ST32", "截图选区裁剪", "平台", caseSnipCrop},
		{"ST33", "联网搜索结果解析", "网络", caseWebSearchParse},
		{"ST34", "跨会话历史检索", "检索", caseHistorySearch},
		{"ST35", "构建报错结构化", "诊断", caseDiagStructured},
		{"ST36", "子代理结论预算", "成本", caseSubAgentBudget},
		{"ST37", "模型路由与分账", "成本", caseModelRouter},
		{"ST38", "工具卡分组折叠", "平台", caseToolFold},
		{"ST39", "目标收敛循环", "执行", caseGoalLoop},
		{"ST40", "Git 提交与回滚闭环", "状态", caseGitFlow},
		{"ST41", "并行写子代理隔离合并", "执行", caseParallelMerge},
		{"ST42", "覆盖率与性能基线", "评测", caseBaseline},
		{"ST43", "结构化数据查询", "检索", caseDataTable},
		{"ST44", "图片理解与任务侧栏", "平台", caseImageAndSidebar},
		{"ST45", "后台任务与进程托管", "执行", caseBackgroundTasks},
		{"ST46", "审批策略与目录白名单", "安全", casePermPolicy},
		{"ST47", "工具调用可靠性", "执行", caseToolReliability},
		{"ST48", "自定义子代理定义", "扩展", caseCustomAgents},
		{"ST49", "补丁逐段审阅", "文件", casePatchHunks},
		{"ST50", "项目长期记忆", "状态", caseProjectMemory},
		{"ST51", "会话分叉隔离", "状态", caseForkIsolation},
		{"ST52", "远程 MCP 传输", "扩展", caseRemoteMCP},
		{"ST53", "网络分层诊断", "网络", caseNetDiagnose},
		{"ST54", "图片压缩与 OCR", "平台", caseImageCompress},
		{"ST55", "定时任务排期与落盘", "自动化", caseScheduleStore},
		{"ST56", "目录守护变化检出", "自动化", caseWatchDiff},
		{"ST57", "手机指挥台鉴权", "自动化", caseRemoteConsole},
		{"ST58", "更新检查与包校验", "网络", caseUpdateCheck},
		{"ST59", "索引缓存指纹校验", "检索", caseIndexCache},
		{"ST60", "字号缩放与主题边界", "平台", caseScaleTheme},
		{"ST61", "二维码矩阵结构不变量", "自动化", caseQRStructure},
		{"ST62", "二维码掩码与罚分选择", "自动化", caseQRMaskChoice},
		{"ST63", "扫码配对地址", "自动化", casePairURL},
		{"ST64", "指挥台自签 HTTPS", "自动化", caseRemoteHTTPS},
		{"ST65", "手机查看会话与输出", "自动化", caseRemoteConvView},
		{"ST66", "手机审批回传", "自动化", caseRemoteApproval},
		{"ST67", "断线重连续传", "自动化", caseRemoteResume},
		{"ST68", "定时任务关机补跑", "自动化", caseScheduleCatchup},
		{"ST69", "守护静默窗口合并", "自动化", caseWatchQuietGap},
		{"ST70", "失败诊断包与配置落盘", "诊断", caseAutoDiagAndConfig},
		{"ST71", "手机停止任务", "自动化", caseRemoteStop},
		{"ST72", "手机计划与设备管理", "自动化", caseRemotePlanDevice},
		{"ST73", "设备表落盘与吊销", "自动化", caseDeviceRegistry},
		{"ST74", "运行指标与耗时画像", "诊断", caseRuntimeMetrics},
		{"ST75", "任务排队不丢", "自动化", caseTaskQueue},
		{"ST76", "更新暂存与架构校验", "网络", caseUpdateStage},
		{"ST77", "配置与清单损坏留证", "诊断", caseConfigQuarantine},
	}
}

// call 调用一个已注册工具。
func (e *env) call(name string, args map[string]interface{}) (tools.Result, error) {
	t, ok := tools.Find(name)
	if !ok {
		return tools.Result{}, fmt.Errorf("工具 %s 未注册", name)
	}
	ctx := &tools.Context{
		WorkDir: e.workDir,
		Confirm: func(string, string) bool { return true },
	}
	return t.Run(ctx, args)
}

func caseToolRegistry(e *env) (Status, string, string) {
	all := tools.All()
	if len(all) < 12 {
		return StatusFail, fmt.Sprintf("只注册了 %d 个工具（期望 ≥12）", len(all)),
			"构建不完整或被裁剪，请用官方完整包"
	}
	var names []string
	for _, t := range all {
		if strings.TrimSpace(t.Name) == "" {
			return StatusFail, "存在空名工具", "这是缺陷，请回传报告"
		}
		var probe map[string]interface{}
		if err := json.Unmarshal([]byte(t.Schema), &probe); err != nil {
			return StatusFail, fmt.Sprintf("工具 %s 的 schema 不是合法 JSON：%v", t.Name, err),
				"这是缺陷，请回传报告"
		}
		if strings.TrimSpace(t.Desc) == "" {
			return StatusFail, fmt.Sprintf("工具 %s 缺少描述", t.Name), "这是缺陷，请回传报告"
		}
		names = append(names, t.Name)
	}
	return StatusPass, fmt.Sprintf("%d 个工具，schema 与描述完整：%s", len(all), strings.Join(names, ",")), ""
}

func caseDataDirWritable(e *env) (Status, string, string) {
	probe := filepath.Join(e.dataDir, ".selftest_probe")
	if err := os.MkdirAll(e.dataDir, 0o755); err != nil {
		return StatusFail, fmt.Sprintf("数据目录无法创建：%v", err), "检查 %APPDATA% 权限或换便携模式"
	}
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return StatusFail, fmt.Sprintf("数据目录不可写：%v", err),
			"多为安全软件拦截或目录只读，把程序换到用户目录下运行"
	}
	os.Remove(probe)
	return StatusPass, "数据目录可读写：" + e.dataDir, ""
}

func caseDiskRoundTrip(e *env) (Status, string, string) {
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i % 251)
	}
	p := e.path("disk_probe.bin")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return StatusFail, fmt.Sprintf("写入 1MB 失败：%v", err), "磁盘可能已满或无写权限"
	}
	back, err := os.ReadFile(p)
	os.Remove(p)
	if err != nil {
		return StatusFail, fmt.Sprintf("读回失败：%v", err), ""
	}
	if len(back) != len(data) || back[len(back)-1] != data[len(data)-1] {
		return StatusFail, fmt.Sprintf("内容不一致：写入 %d 字节，读回 %d 字节", len(data), len(back)),
			"磁盘故障或杀毒软件篡改，建议换盘验证"
	}
	return StatusPass, "1MB 二进制往返一致", ""
}

func caseWriteRead(e *env) (Status, string, string) {
	want := "第一行 中文内容\r\nsecond line\r\n第三行 tail"
	res, err := e.call("write_file", map[string]interface{}{
		"path":    "hello.txt",
		"content": want,
	})
	if err != nil {
		return StatusFail, "write_file 失败：" + err.Error(), ""
	}
	_ = res
	out, err := e.call("read_file", map[string]interface{}{"path": "hello.txt"})
	if err != nil {
		return StatusFail, "read_file 失败：" + err.Error(), ""
	}
	if !strings.Contains(out.Output, "第一行 中文内容") || !strings.Contains(out.Output, "second line") {
		return StatusFail, "读回内容与写入不一致：" + brief(out.Output),
			"疑似编码问题，请确认系统区域设置非 Unicode 程序语言是否为中文"
	}
	return StatusPass, "中文与换行完整往返", ""
}

func caseEditFile(e *env) (Status, string, string) {
	if _, err := e.call("write_file", map[string]interface{}{
		"path": "edit.txt", "content": "alpha\nbeta\ngamma",
	}); err != nil {
		return StatusFail, "准备文件失败：" + err.Error(), ""
	}
	if _, err := e.call("edit_file", map[string]interface{}{
		"path": "edit.txt", "old_string": "beta", "new_string": "beta-v2",
	}); err != nil {
		return StatusFail, "edit_file 失败：" + err.Error(), ""
	}
	out, _ := e.call("read_file", map[string]interface{}{"path": "edit.txt"})
	if !strings.Contains(out.Output, "beta-v2") || strings.Contains(out.Output, "\nbeta\n") {
		return StatusFail, "替换结果不符：" + brief(out.Output), ""
	}
	// 反向用例：不存在的原文必须报错，不能静默成功
	if _, err := e.call("edit_file", map[string]interface{}{
		"path": "edit.txt", "old_string": "not-exist-xyz", "new_string": "x",
	}); err == nil {
		return StatusFail, "替换不存在的文本却返回成功", "这是缺陷：编辑工具缺少匹配校验"
	}
	return StatusPass, "唯一匹配替换生效，不匹配时正确报错", ""
}

func caseListAndGlob(e *env) (Status, string, string) {
	// 自建夹具，不依赖其它用例的遗留文件（用例按独立子目录隔离）
	for _, f := range []struct{ name, content string }{
		{"hello.txt", "list-glob fixture"},
		{"edit.txt", "alpha\nbeta\ngamma"},
	} {
		if _, err := e.call("write_file", map[string]interface{}{
			"path": f.name, "content": f.content,
		}); err != nil {
			return StatusFail, "准备文件失败：" + err.Error(), ""
		}
	}
	res, err := e.call("list_dir", map[string]interface{}{"path": "."})
	if err != nil {
		return StatusFail, "list_dir 失败：" + err.Error(), ""
	}
	if !strings.Contains(res.Output, "hello.txt") {
		return StatusFail, "目录列举未包含 hello.txt：" + brief(res.Output), ""
	}
	g, err := e.call("glob", map[string]interface{}{"pattern": "*.txt", "path": "."})
	if err != nil {
		return StatusFail, "glob 失败：" + err.Error(), ""
	}
	if !strings.Contains(g.Output, "edit.txt") {
		return StatusFail, "通配符未命中 edit.txt：" + brief(g.Output), ""
	}
	return StatusPass, "目录列举与通配符均命中", ""
}

func caseGrep(e *env) (Status, string, string) {
	// 自建夹具（用例独立子目录，之前其它用例写过的 edit.txt 这里看不到）
	if _, err := e.call("write_file", map[string]interface{}{
		"path": "edit.txt", "content": "alpha\nbeta\ngamma",
	}); err != nil {
		return StatusFail, "准备文件失败：" + err.Error(), ""
	}
	res, err := e.call("grep", map[string]interface{}{
		"pattern": "gamma", "path": ".", "max_results": float64(20),
	})
	if err != nil {
		return StatusFail, "grep 失败：" + err.Error(), ""
	}
	if !strings.Contains(res.Output, "edit.txt") {
		return StatusFail, "未命中 edit.txt：" + brief(res.Output), ""
	}
	return StatusPass, "正则检索命中并带文件名", ""
}

func caseCodeSearch(e *env) (Status, string, string) {
	src := "package sample\n\nfunc SelftestProbeSymbol(n int) int {\n\treturn n * 2\n}\n"
	if err := os.MkdirAll(filepath.Join(e.workDir, "sample"), 0o755); err != nil {
		return StatusFail, "建目录失败：" + err.Error(), ""
	}
	if err := os.WriteFile(filepath.Join(e.workDir, "sample", "probe.go"), []byte(src), 0o644); err != nil {
		return StatusFail, "写探测文件失败：" + err.Error(), ""
	}
	res, err := e.call("code_search", map[string]interface{}{
		"query": "SelftestProbeSymbol", "refresh": true,
	})
	if err != nil {
		return StatusFail, "code_search 失败：" + err.Error(), "代码索引不可用，符号检索会退化"
	}
	if !strings.Contains(res.Output, "SelftestProbeSymbol") {
		return StatusFail, "未解析出符号：" + brief(res.Output), "语法解析异常"
	}
	return StatusPass, "Go 符号解析命中 probe.go", ""
}

func caseRunCommand(e *env) (Status, string, string) {
	cmd := "echo selftest-ok"
	if runtime.GOOS == "windows" {
		cmd = "cmd /c echo selftest-ok"
	}
	res, err := e.call("run_command", map[string]interface{}{
		"command": cmd, "timeout": float64(20),
	})
	if err != nil {
		return StatusFail, "run_command 失败：" + err.Error(), "命令执行通道不可用，所有依赖编译/测试的能力都会失效"
	}
	if !strings.Contains(res.Output, "selftest-ok") {
		return StatusFail, "未捕获到输出：" + brief(res.Output), "管道读取异常"
	}
	return StatusPass, "cmd/sh 通道可用，输出已捕获", ""
}

func caseForbiddenPath(e *env) (Status, string, string) {
	bad := `C:\Windows\System32\drivers\etc\hosts`
	if !core.IsForbiddenPath(bad) {
		return StatusFail, "系统目录未被识别为保护路径：" + bad,
			"防护失效，存在误删系统文件风险，请立即停止使用并回传报告"
	}
	if core.IsForbiddenPath(e.path("normal.txt")) {
		return StatusFail, "普通工作区文件被误判为系统目录：" + e.path("normal.txt"),
			"保护范围过宽会挡住正常读写"
	}
	// 越界路径必须能被识别：工作区内为 false，工作区外为 true
	if core.EscapesWorkDir(e.workDir, e.path("normal.txt")) {
		return StatusFail, "工作区内文件被误判为越界：" + e.path("normal.txt"), "保护范围过宽会挡住正常读写"
	}
	outside := filepath.Join(e.workDir, "..", "outside.txt")
	if !core.EscapesWorkDir(e.workDir, outside) {
		return StatusFail, "工作区外路径未被识别为越界：" + outside,
			"防护失效：agent 可用 ../ 改到工作区之外，请立即回传报告"
	}
	// 相对路径解析后必须落在工作区内（../ 被规范化掉，由越界判定兜底）
	resolved := core.ResolvePath(e.workDir, "sub/../inside.txt")
	if core.EscapesWorkDir(e.workDir, resolved) {
		return StatusFail, "普通相对路径被解析到工作区外：" + resolved, ""
	}
	return StatusPass, "系统目录拦截、越界识别、相对路径归一均正常", ""
}

func caseCommandRisk(e *env) (Status, string, string) {
	checks := []struct {
		cmd  string
		want core.RiskLevel
	}{
		{"rm -rf /", core.RiskDanger},
		{`del /f /s /q C:\*`, core.RiskDanger},
		{"format c:", core.RiskDanger},
		{"reg delete HKLM\\Software\\x", core.RiskDanger},
		{"echo hello", core.RiskSafe},
		{"mkdir build", core.RiskCaution},
	}
	for _, c := range checks {
		if got := core.CommandRisk(c.cmd); got != c.want {
			return StatusFail, fmt.Sprintf("命令 %q 分级为 %s，期望 %s", c.cmd, got, c.want),
				"危险命令分级不准，可能导致误放行"
		}
	}
	return StatusPass, "6 条样本分级全部正确（含 4 条破坏性命令）", ""
}

func caseSnapshotRestore(e *env) (Status, string, string) {
	p := e.path("snap.txt")
	if err := os.WriteFile(p, []byte("v1-original"), 0o644); err != nil {
		return StatusFail, "准备文件失败：" + err.Error(), ""
	}
	before := core.SnapshotCount()
	if err := core.SnapshotFile(p); err != nil {
		return StatusFail, "快照失败：" + err.Error(), "回滚能力不可用，改坏文件将无法恢复"
	}
	after := core.SnapshotCount()
	// 快照目录最多保留 200 条：到顶之后每新增一条会裁掉最旧一条，计数不变。
	// 因此不能把"计数增加"当作落盘判据（老机器上跑过几轮自检就会到顶，从而假失败），
	// 真正的判据是下面这次还原能不能成功。
	capped := after <= before
	restored := false
	// 还原成功时快照已被消耗，无需清理；其余情况只清掉本次新增的那一条，
	// 绝不碰用户既有快照
	defer func() {
		if !restored {
			core.DiscardLastSnapshot()
		}
	}()

	if err := os.WriteFile(p, []byte("v2-modified"), 0o644); err != nil {
		return StatusFail, "写入修改失败：" + err.Error(), ""
	}
	ok, err := core.RestoreLatestFor(p)
	if err != nil {
		return StatusFail, "还原失败：" + err.Error(), "快照文件可能被安全软件删除"
	}
	if !ok {
		return StatusFail, "还原返回 false，未找到该文件快照", ""
	}
	data, _ := os.ReadFile(p)
	if string(data) != "v1-original" {
		return StatusFail, "还原后内容为 " + string(data) + "，期望 v1-original", "回滚逻辑有缺陷"
	}
	restored = true
	extra := ""
	if capped {
		extra = "，快照已达 200 条上限（新增即裁旧，计数不变属正常）"
	}
	return StatusPass, fmt.Sprintf("快照 → 改动 → 还原成功（快照数 %d→%d%s）", before, after, extra), ""
}

func caseAtomicAndGzip(e *env) (Status, string, string) {
	p := e.path("atomic.txt")
	payload := []byte(strings.Repeat("重复内容用于压缩测试-", 500))
	if err := core.WriteFileAtomic(p, payload, 0o644); err != nil {
		return StatusFail, "原子写失败：" + err.Error(), "断电/崩溃保护不可用"
	}
	back, err := os.ReadFile(p)
	if err != nil || len(back) != len(payload) {
		return StatusFail, fmt.Sprintf("原子写读回异常：err=%v len=%d/%d", err, len(back), len(payload)), ""
	}
	packed := core.GzipIfSmaller(payload, 256)
	if !core.IsGzipData(packed) {
		return StatusFail, "可压缩内容未被压缩", "会话历史压缩失效，长会话会更快占内存"
	}
	out, err := core.GunzipData(packed, 1<<20)
	if err != nil || string(out) != string(payload) {
		return StatusFail, fmt.Sprintf("解压往返不一致：err=%v", err), ""
	}
	return StatusPass, fmt.Sprintf("原子写 %d 字节，压缩 %d→%d 并可完整解压", len(payload), len(payload), len(packed)), ""
}

func caseUnicodePath(e *env) (Status, string, string) {
	dir := filepath.Join(e.workDir, "中文 目录")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StatusFail, "创建中文目录失败：" + err.Error(), "系统区域设置可能影响非 ASCII 路径"
	}
	rel := filepath.Join("中文 目录", "报告 v1.txt")
	if _, err := e.call("write_file", map[string]interface{}{"path": rel, "content": "内容 OK"}); err != nil {
		return StatusFail, "写中文路径失败：" + err.Error(), ""
	}
	res, err := e.call("read_file", map[string]interface{}{"path": rel})
	if err != nil {
		return StatusFail, "读中文路径失败：" + err.Error(), ""
	}
	if !strings.Contains(res.Output, "内容 OK") {
		return StatusFail, "中文路径读回内容异常：" + brief(res.Output), ""
	}
	return StatusPass, "中文目录 + 空格文件名读写正常", ""
}

func caseProvider(e *env) (Status, string, string) {
	if e.opt.ProviderTest == nil {
		return StatusSkip, "本次运行未启用模型连通性探测（仅命令行无配置时跳过）", ""
	}
	if err := e.opt.ProviderTest(); err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return StatusSkip, "尚未配置模型，跳过连通性探测", "去设置页填接口地址、API Key、模型名后可再跑一次"
		}
		msg := err.Error()
		hint := "设置页检查接口地址、API Key、模型名后点测试连接"
		if strings.Contains(strings.ToLower(msg), "certificate") || strings.Contains(msg, "x509") {
			hint = "证书链不受信任：Win7 需安装 KB4474419 与 KB4490628 补丁"
		}
		return StatusFail, "模型不可用：" + msg, hint
	}
	return StatusPass, "模型请求成功（含一次真实对话）", ""
}

func brief(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " | ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	if s == "" {
		return "(空)"
	}
	return s
}

// caseCheckpoints 验证"改动清单"能列出、且能一次回退多步（v1.20 checkpoint）。
func caseCheckpoints(e *env) (Status, string, string) {
	dir := filepath.Join(e.workDir, "cp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StatusFail, "建目录失败：" + err.Error(), "检查工作区权限"
	}
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("v1"), 0o644); err != nil {
		return StatusFail, "预置文件失败：" + err.Error(), "检查工作区权限"
	}
	// 两次改动：a 被覆盖、b 新建
	if err := core.SnapshotFile(a); err != nil {
		return StatusFail, "快照失败：" + err.Error(), "检查数据目录可写性"
	}
	if err := os.WriteFile(a, []byte("v2"), 0o644); err != nil {
		return StatusFail, "改写失败：" + err.Error(), "检查工作区权限"
	}
	if err := core.SnapshotFile(b); err != nil {
		return StatusFail, "快照失败：" + err.Error(), "检查数据目录可写性"
	}
	if err := os.WriteFile(b, []byte("new"), 0o644); err != nil {
		return StatusFail, "新建失败：" + err.Error(), "检查工作区权限"
	}

	cps := core.ListCheckpoints(5)
	if len(cps) < 2 {
		return StatusFail, fmt.Sprintf("清单只有 %d 条（期望 ≥2）", len(cps)),
			"快照没有落盘，检查数据目录/snapshots"
	}
	if !strings.HasSuffix(cps[0].Path, "b.txt") {
		return StatusFail, "最新一条不是 b.txt，排序有误：" + cps[0].Path, "检查 ListCheckpoints 排序"
	}

	done, err := core.RewindSteps(2)
	if err != nil {
		return StatusFail, "回退失败：" + err.Error(), "检查快照文件是否完整"
	}
	if len(done) != 2 {
		return StatusFail, fmt.Sprintf("只回退了 %d 步", len(done)), "检查 RewindSteps 循环"
	}
	if got, _ := os.ReadFile(a); string(got) != "v1" {
		return StatusFail, "a.txt 内容未还原：" + string(got), "检查 UndoLast 还原逻辑"
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		return StatusFail, "b.txt 未被删除", "检查新建文件的撤销逻辑"
	}
	return StatusPass, "2 步改动可列出并一次性回退", ""
}

// caseDiagnose 验证失败诊断能把常见错误归类，并给出可执行的下一步。
func caseDiagnose(e *env) (Status, string, string) {
	table := []struct {
		err  string
		want string
	}{
		{"错误：文件不存在: C:\\tmp\\x.txt", "not_found"},
		{"错误：access is denied.", "permission"},
		{"错误：待替换内容出现了 0 次，不唯一或不存在", "anchor"},
		{"错误：'git' 不是内部或外部命令", "command_missing"},
		{"错误：当前处于计划模式（只读），已拒绝执行 write_file", "guard"},
	}
	var bad []string
	for _, c := range table {
		d := agent.Diagnose("test", c.err)
		if d.Kind != c.want {
			bad = append(bad, fmt.Sprintf("%q→%s(期望 %s)", c.err, d.Kind, c.want))
		}
		if d.Next == "" {
			bad = append(bad, "缺少建议："+c.err)
		}
	}
	if len(bad) > 0 {
		return StatusFail, strings.Join(bad, "；"), "检查 diagRules 关键词表"
	}
	// 未命中任何规则时必须落到 unknown，而不是误判成某类
	if d := agent.Diagnose("test", "错误：莫名其妙的失败"); d.Kind != "unknown" {
		return StatusFail, "未知错误被误判为 " + d.Kind, "检查兜底分支"
	}
	return StatusPass, fmt.Sprintf("%d 类常见错误均可归类", len(table)), ""
}

// casePlanDetect 验证"方案文本"能被识别，普通回答不会误触发计划卡片。
// caseSkills 验证技能目录能被发现、frontmatter 能解析、正文能取到。
func caseSkills(e *env) (Status, string, string) {
	dir := skills.ProjectDir(e.workDir)
	sub := filepath.Join(dir, "st-skill")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return StatusFail, "无法创建技能目录：" + err.Error(), "检查工作目录写权限"
	}
	doc := "---\nname: st-skill\ndescription: 自检用技能\nwhen: 自检时\n---\n第一步：跑测试。\n第二步：读报错。\n"
	if err := os.WriteFile(filepath.Join(sub, "SKILL.md"), []byte(doc), 0o644); err != nil {
		return StatusFail, "写技能文件失败：" + err.Error(), "检查磁盘权限"
	}
	defer os.RemoveAll(sub)

	list := skills.Scan(e.workDir)
	var found *skills.Skill
	for i := range list {
		if list[i].Name == "st-skill" {
			found = &list[i]
		}
	}
	if found == nil {
		return StatusFail, "技能未被扫描到", "检查 skills.Scan 的目录约定"
	}
	if !strings.Contains(found.Body, "跑测试") {
		return StatusFail, "技能正文解析异常", "检查 frontmatter 分隔符解析"
	}
	res, err := e.call("skill_use", map[string]interface{}{"name": "st-skill"})
	if err != nil {
		return StatusFail, "skill_use 调用失败：" + err.Error(), "检查工具注册"
	}
	if !strings.Contains(res.Output, "读报错") {
		return StatusFail, "skill_use 未返回正文", "检查 Find 的匹配逻辑"
	}
	if blk := skills.Block(e.workDir); !strings.Contains(blk, "st-skill") {
		return StatusFail, "系统提示未注入技能清单", "检查 buildContext 是否拼接 skills.Block"
	}
	return StatusPass, "技能可发现、正文可加载、提示已注入", ""
}

// caseIndexIncremental 验证写完文件后索引能同步，且脏文件被强制重解析。
func caseIndexIncremental(e *env) (Status, string, string) {
	codeidx.Reset()
	defer codeidx.Reset()
	f := filepath.Join(e.workDir, "st_index.go")
	if err := os.WriteFile(f, []byte("package st\n\nfunc Alpha() int { return 1 }\n"), 0o644); err != nil {
		return StatusFail, "写测试文件失败：" + err.Error(), "检查磁盘权限"
	}
	defer os.Remove(f)
	ix := codeidx.Build(e.workDir, nil)
	if len(ix.Search("Alpha", 5)) == 0 {
		return StatusFail, "首次索引未找到符号 Alpha", "检查 parseGoFile"
	}
	if err := os.WriteFile(f, []byte("package st\n\nfunc Alpha() int { return 1 }\n\nfunc Beta() int { return 2 }\n"), 0o644); err != nil {
		return StatusFail, "改写测试文件失败：" + err.Error(), "检查磁盘权限"
	}
	codeidx.MarkDirtyAbs(f)
	st := codeidx.Update(e.workDir)
	hits := codeidx.Cached().Search("Beta", 5)
	if len(hits) == 0 {
		return StatusFail, fmt.Sprintf("增量后仍无 Beta（dirty=%d parsed=%d）", st.Dirty, st.Parsed),
			"检查 Build 里脏文件是否绕过 mtime 复用"
	}
	if hits[0].Line != 5 {
		return StatusFail, fmt.Sprintf("Beta 行号应为 5，实得 %d", hits[0].Line), "检查符号排序与行号记录"
	}
	if st.Dirty < 1 {
		return StatusFail, "脏文件未被计为强制重解析", "检查 takeDirty 与 isDirty 判定"
	}
	return StatusPass, fmt.Sprintf("增量重建正常：复用 %d / 重解析 %d（脏 %d）", st.Reused, st.Parsed, st.Dirty), ""
}

// caseEvalBaseline 跑一遍评测基线，确认用例集能执行且全部通过。
func caseEvalBaseline(e *env) (Status, string, string) {
	if n := len(eval.Cases()); n < 9 {
		return StatusFail, fmt.Sprintf("评测用例只有 %d 个（期望 ≥9）", n), "检查 eval.Cases"
	}
	root := filepath.Join(e.workDir, "st_eval")
	defer os.RemoveAll(root)
	rep := eval.Run(root, false)
	if rep.Fail > 0 {
		var ids []string
		for _, r := range rep.Results {
			if r.Status == eval.StatusFail {
				ids = append(ids, r.ID)
			}
		}
		return StatusFail, "评测有失败用例：" + strings.Join(ids, ", "),
			"打开评测报告看失败详情"
	}
	return StatusPass, fmt.Sprintf("%d 个用例全部通过，耗时 %.2fs", len(rep.Results), float64(rep.TotalMs)/1000), ""
}

func casePlanDetect(e *env) (Status, string, string) {
	plan := "我打算这样改：\n1. 先读 internal/app/app.go 里的事件分发\n" +
		"2. 在 plan 分支补一个确认条\n3. 最后跑一遍 selftest 确认没回归\n" +
		"改完大约涉及两个文件，不动构建脚本。"
	if !agent.LooksLikePlan(plan) {
		return StatusFail, "编号方案未被识别", "检查 looksLikePlan 的编号行判定"
	}
	short := "1. 是的\n2. 没问题"
	if agent.LooksLikePlan(short) {
		return StatusFail, "过短文本被误判为方案", "检查最小长度阈值"
	}
	plain := "这个函数的作用是把事件按类型分发，处理完统一刷新界面版本号，没有副作用。"
	if agent.LooksLikePlan(plain) {
		return StatusFail, "普通回答被误判为方案", "收紧判定条件"
	}
	return StatusPass, "方案识别与误判拦截均正常", ""
}

// caseRunTests 验证 run_tests 已注册，且四个框架的输出都能解析出失败用例与文件行号。
func caseRunTests(e *env) (Status, string, string) {
	if _, ok := tools.Find("run_tests"); !ok {
		return StatusFail, "run_tests 未注册", "检查 internal/tools/runtests.go 的 init"
	}
	goOut := "--- FAIL: TestAdd (0.00s)\n    calc_test.go:42: got 3 want 4\nFAIL\nFAIL\texample.com/calc\t0.01s\n"
	rep := tools.ParseTestReport("go", goOut, 1)
	if !strings.Contains(rep, "TestAdd") || !strings.Contains(rep, "calc_test.go:42") {
		return StatusFail, "Go 失败解析丢了用例名或行号：" + strings.ReplaceAll(rep, "\n", " | "),
			"检查 parseTestOutput 的 go 分支"
	}
	pyOut := "FAILED tests/test_a.py::test_login - AssertionError: 401 != 200\n========= 1 failed, 4 passed in 0.3s =========\n"
	rep = tools.ParseTestReport("pytest", pyOut, 1)
	if !strings.Contains(rep, "tests/test_a.py::test_login") || !strings.Contains(rep, "401") {
		return StatusFail, "pytest 失败解析异常：" + strings.ReplaceAll(rep, "\n", " | "), "检查 pytest 分支"
	}
	jestOut := "  \u2715 adds numbers (5 ms)\nTests:       1 failed, 1 passed, 2 total\n"
	if rep := tools.ParseTestReport("npm", jestOut, 1); !strings.Contains(rep, "通过 1 / 失败 1") {
		return StatusFail, "jest 统计解析异常：" + strings.ReplaceAll(rep, "\n", " | "), "检查 numBefore 解析"
	}
	cargoOut := "test math::div ... FAILED\n\ntest result: FAILED. 0 passed; 1 failed\n"
	if rep := tools.ParseTestReport("cargo", cargoOut, 101); !strings.Contains(rep, "math::div") {
		return StatusFail, "cargo 失败解析异常：" + strings.ReplaceAll(rep, "\n", " | "), "检查 cargo 分支"
	}
	if rep := tools.ParseTestReport("go", "ok  \texample.com/a\t0.01s\n", 0); !strings.Contains(rep, "结论: 通过") {
		return StatusFail, "全通过输出被判成失败：" + strings.ReplaceAll(rep, "\n", " | "), "检查结论判定"
	}
	return StatusPass, "四个框架的失败用例、行号与统计均解析正确", ""
}

// caseWebFetch 验证网页正文提取（去脚本样式、解实体、块级换行）与内网地址拦截。
func caseWebFetch(e *env) (Status, string, string) {
	if _, ok := tools.Find("web_fetch"); !ok {
		return StatusFail, "web_fetch 未注册", "检查 internal/tools/webfetch.go 的 init"
	}
	html := "<html><head><title>标题A</title><style>b{}</style><script>var x=1;</script></head>" +
		"<body><h1>小标题</h1><p>第一段 &amp; 第二句</p><ul><li>条目一</li><li>条目二</li></ul></body></html>"
	title, text := tools.ExtractWebText(html)
	if title != "标题A" {
		return StatusFail, "标题提取错误：" + title, "检查 pageTitle"
	}
	for _, want := range []string{"小标题", "第一段 & 第二句", "- 条目一"} {
		if !strings.Contains(text, want) {
			return StatusFail, "正文缺少片段：" + want + "（实际：" + strings.ReplaceAll(text, "\n", " | ") + "）",
				"检查 htmlToText"
		}
	}
	for _, bad := range []string{"var x=1", "b{}</style"} {
		if strings.Contains(text, bad) {
			return StatusFail, "脚本或样式未被剔除：" + bad, "检查 reDrop 正则"
		}
	}
	blocked := []string{"http://127.0.0.1/a", "http://localhost:8080/", "http://169.254.169.254/latest/", "file:///C:/Windows/win.ini"}
	for _, u := range blocked {
		if err := tools.CheckFetchURL(u); err == nil {
			return StatusFail, "未拦截内网/非法地址：" + u, "检查 checkPublicURL 与 isPublicIP"
		}
	}
	if err := tools.CheckFetchURL("https://8.8.8.8/"); err != nil {
		return StatusFail, "公网地址被误拦：" + err.Error(), "检查 isPublicIP 判定"
	}
	return StatusPass, "正文提取与 SSRF 拦截均正常（含元数据地址）", ""
}

// caseLowMemory 验证低内存档位能把各处阈值收下来，且 @ 引用预算真的变小。
func caseLowMemory(e *env) (Status, string, string) {
	was := core.LowMemory()
	defer core.SetLowMemory(was)

	core.SetLowMemory(false)
	std := core.MemProfile()
	core.SetLowMemory(true)
	low := core.MemProfile()

	if low.AtRefBudget >= std.AtRefBudget {
		return StatusFail, fmt.Sprintf("低内存档 @ 引用预算未收紧：%d → %d", std.AtRefBudget, low.AtRefBudget),
			"检查 MemProfile 的低内存分支"
	}
	if low.ToolOutput >= std.ToolOutput {
		return StatusFail, "低内存档工具输出上限未收紧", "检查 MemProfile.ToolOutput"
	}
	if low.IndexFiles >= std.IndexFiles || low.ImagesPerMessage >= std.ImagesPerMessage {
		return StatusFail, "低内存档索引或图片上限未收紧", "检查 MemProfile 各字段"
	}
	if low.SubTasks > std.SubTasks || low.ImagesPerMessage < 1 {
		return StatusFail, "低内存档子任务并发异常", "检查 MemProfile.SubTasks"
	}
	// 档位必须"真的接上"，不能只改数字不改用法。
	// SubAgentTurns 曾经就是这种死字段：说明里写着低内存档子代理只跑 8 轮，
	// 代码里 subagent 用的是一个写死的常量，一点没变。
	if got := agent.DefaultSubRounds(); got != low.SubAgentTurns {
		return StatusFail, fmt.Sprintf("子代理轮数未按档位生效：档位 %d，实际 %d", low.SubAgentTurns, got),
			"检查 agent.DefaultSubRounds 是否被 subagent 使用"
	}

	// @ 引用预算实测：同一个大文件，低内存档注入的内容必须更少
	root := filepath.Join(e.workDir, "st_mem")
	defer os.RemoveAll(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return StatusFail, "建临时目录失败：" + err.Error(), "检查磁盘权限"
	}
	big := filepath.Join(root, "big.txt")
	var sb strings.Builder
	for i := 0; i < 4000; i++ {
		sb.WriteString("0123456789abcdef\n")
	}
	if err := os.WriteFile(big, []byte(sb.String()), 0o644); err != nil {
		return StatusFail, "写测试文件失败：" + err.Error(), "检查磁盘权限"
	}
	ref := "@big.txt"

	core.SetLowMemory(false)
	bigOut := core.ExpandAtRefs(ref, root)
	core.SetLowMemory(true)
	smallOut := core.ExpandAtRefs(ref, root)
	if len(smallOut) >= len(bigOut) {
		return StatusFail, fmt.Sprintf("低内存档 @ 引用注入未减少：%d → %d 字节", len(bigOut), len(smallOut)),
			"检查 ExpandAtRefs 是否读取 MemProfile"
	}
	if len(smallOut) > low.AtRefBudget+2048 {
		return StatusFail, fmt.Sprintf("低内存档注入 %d 字节，超过预算 %d", len(smallOut), low.AtRefBudget),
			"检查 ExpandAtRefs 的预算截断"
	}
	return StatusPass, fmt.Sprintf("@ 引用 %d → %d 字节，索引 %d → %d 文件，图片 %d → %d 张",
		len(bigOut), len(smallOut), std.IndexFiles, low.IndexFiles, std.ImagesPerMessage, low.ImagesPerMessage), ""
}
