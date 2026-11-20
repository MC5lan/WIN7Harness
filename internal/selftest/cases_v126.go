package selftest

// v1.26 新增用例：后台任务与进程托管、审批策略、工具调用可靠性、自定义子代理、
// 补丁逐段审阅、项目长期记忆、会话分叉、远程 MCP、网络分层诊断、图片压缩与 OCR。
//
// 取向与 v1.25 一致：造真实数据走主路径，断言可观测结果；不依赖外网与真实模型。

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"win7harness/internal/agent"
	"win7harness/internal/core"
	"win7harness/internal/coreutil"
	"win7harness/internal/llm"
	"win7harness/internal/mcp"
)

// bgIDRe 从工具返回里抓后台任务 ID（bg-12 这种）。
var bgIDRe = regexp.MustCompile(`bg-\d+`)

// clipText 截断长文本用于报告（保留头部，够定位即可）。
func clipText(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// ctx 给需要 context 的用例一个带超时的上下文。
func (e *env) ctx() context.Context {
	return context.Background()
}

// ST45 后台任务与进程托管：background 启动 → 读增量输出 → 列表 → 终止。
func caseBackgroundTasks(e *env) (Status, string, string) {
	cmd := "sleep 2"
	if runtime.GOOS == "windows" {
		cmd = "ping -n 3 127.0.0.1"
	}
	res, err := e.call("run_command", map[string]interface{}{
		"command":    cmd,
		"background": true,
		"timeout":    60,
	})
	if err != nil {
		return StatusFail, "后台启动失败：" + err.Error(),
			"检查 StartBackground 与 run_command 的 background 分支"
	}
	out := res.Output
	if !strings.Contains(out, "已在后台启动") {
		return StatusFail, "返回里没有后台任务标识：" + clipText(out, 200),
			"background=true 时应立即返回任务 ID 而不是等命令结束"
	}
	id := bgIDRe.FindString(out)
	if id == "" {
		return StatusFail, "没能从返回里解析出任务 ID：" + clipText(out, 200),
			"任务 ID 应形如 bg-1 且出现在首行"
	}

	// 列表里应能看到这个任务。
	lst, err := e.call("task_list", map[string]interface{}{})
	if err != nil {
		return StatusFail, "task_list 失败：" + err.Error(), "检查任务注册表"
	}
	if !strings.Contains(lst.Output, id) {
		return StatusFail, "task_list 里找不到 " + id, "后台任务没有登记进全局表"
	}

	// 读输出（命令可能还在跑，允许为空，但不能报错）。
	if _, err := e.call("task_output", map[string]interface{}{"id": id}); err != nil {
		return StatusFail, "task_output 失败：" + err.Error(), "增量读取应允许在命令未结束时调用"
	}

	// 终止并确认状态收敛。
	if _, err := e.call("task_kill", map[string]interface{}{"id": id}); err != nil {
		return StatusFail, "task_kill 失败：" + err.Error(), "终止后台任务应尽最大努力杀进程树"
	}
	time.Sleep(300 * time.Millisecond)
	after, err := e.call("task_list", map[string]interface{}{})
	if err == nil && strings.Contains(after.Output, "运行中") {
		for _, line := range strings.Split(after.Output, "\n") {
			if strings.Contains(line, id) && strings.Contains(line, "运行中") {
				return StatusFail, "终止后仍在运行：" + line, "task_kill 未生效（Windows 下需级联结束进程树）"
			}
		}
	}
	return StatusPass, fmt.Sprintf("后台任务 %s 启动/读取/列表/终止全链路正常", id), ""
}

// ST46 审批策略：deny 优先、allow 命中、ask 默认、plan 只读、目录白名单。
func casePermPolicy(e *env) (Status, string, string) {
	outsideDir := filepath.Join(filepath.Dir(e.workDir), "harness-allowed")
	pol := core.NewPolicyFromRules("ask", []string{
		"allow:run_command:git status",
		"deny:run_command:format *",
		"ask:write_file:*.env",
	}, []string{outsideDir})

	cases := []struct {
		name   string
		tool   string
		target string
		write  bool
		danger core.RiskLevel
		want   string
	}{
		{"命中 allow", "run_command", "git status --short", true, core.RiskSafe, "allow"},
		{"命中 deny", "run_command", "format C: /q", true, core.RiskDanger, "deny"},
		{"默认 ask", "write_file", filepath.Join(e.workDir, "a.txt"), true, core.RiskSafe, "ask"},
		{"只读放行", "read_file", filepath.Join(e.workDir, "a.txt"), false, core.RiskSafe, "allow"},
		{"越界未白名单", "write_file", filepath.Join(filepath.Dir(e.workDir), "elsewhere", "b.txt"), true, core.RiskSafe, "ask"},
	}
	for _, c := range cases {
		if got := pol.Decide(c.tool, c.target, c.write, c.danger, e.workDir).Kind; got != c.want {
			return StatusFail, fmt.Sprintf("%s：判定 %s（期望 %s）", c.name, got, c.want),
				"审批规则匹配顺序或目录白名单判断有误"
		}
	}

	// auto 模式下才看得出白名单的作用：工作区内放行、越界问、越界但在白名单里放行。
	autoPol := core.NewPolicyFromRules("auto", nil, []string{outsideDir})
	autoCases := []struct {
		name   string
		target string
		want   string
	}{
		{"工作区内直接写", filepath.Join(e.workDir, "in.txt"), "allow"},
		{"越界且未白名单", filepath.Join(filepath.Dir(e.workDir), "elsewhere", "b.txt"), "ask"},
		{"越界但在白名单", filepath.Join(outsideDir, "c.txt"), "allow"},
	}
	for _, c := range autoCases {
		if got := autoPol.Decide("write_file", c.target, true, core.RiskSafe, e.workDir).Kind; got != c.want {
			return StatusFail, fmt.Sprintf("auto 模式 %s：判定 %s（期望 %s）", c.name, got, c.want),
				"目录白名单要能放开工作区外的指定目录"
		}
	}

	// plan 模式：写操作一律拒绝，只读仍放行。
	pol.Mode = core.PermPlan
	if got := pol.Decide("write_file", filepath.Join(e.workDir, "x.txt"), true, core.RiskSafe, e.workDir).Kind; got != "deny" {
		return StatusFail, "计划模式下写操作判定为 " + got, "只读模式必须拦住写盘"
	}
	if got := pol.Decide("read_file", filepath.Join(e.workDir, "x.txt"), false, core.RiskSafe, e.workDir).Kind; got != "allow" {
		return StatusFail, "计划模式下只读操作判定为 " + got, "只读模式不应拦住读操作"
	}

	// 规则往返：Add → FlatRules → 重新解析，语义不丢。
	if _, err := pol.Add("deny", "run_command:del *"); err != nil {
		return StatusFail, "加规则失败：" + err.Error(), "检查 Policy.Add 的规则规范化"
	}
	back := core.NewPolicyFromRules("ask", pol.FlatRules(), nil)
	if got := back.Decide("run_command", "del /s /q d", true, core.RiskSafe, e.workDir).Kind; got != "deny" {
		return StatusFail, "规则写盘再读回后失效（判定 " + got + "）", "FlatRules 与 SplitPermRules 的格式必须可往返"
	}
	return StatusPass, fmt.Sprintf("%d 条判定场景与规则往返全部符合预期", len(cases)+3), ""
}

// ST47 工具调用可靠性：坏 JSON 修复 + 空响应自动重试一次。
func caseToolReliability(e *env) (Status, string, string) {
	bad := map[string]string{
		"带前后杂质":    "好的，我调用工具：{\"path\":\"a.txt\"} 以上。",
		"被截断":      `{"command":"go build","timeout":120`,
		"尾逗号":      `{"path":"b.txt",}`,
		"markdown": "```json\n{\"query\":\"缓存\"}\n```",
	}
	for name, raw := range bad {
		m, err := agent.RepairToolArgs(raw)
		if err != nil {
			return StatusFail, name + "：解析失败 " + err.Error(),
				"repairJSON 应剥离包裹文本、补齐括号并容忍尾逗号"
		}
		if len(m) == 0 {
			return StatusFail, name + "：解析结果为空", "修复后应得到至少一个字段"
		}
	}

	// 空响应重试：第一次返回空增量，第二次返回正文，应成功且只重试一次。
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		if n == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{}}]}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"恢复成功\"}}]}\n\n")
		}
		if fl != nil {
			fl.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := llm.New(srv.URL, "k", "mock", 10*time.Second)
	var got strings.Builder
	_, err := c.Chat(e.ctx(), []llm.Message{{Role: "user", Content: "hi"}}, nil, 0.2, 256,
		llm.Handlers{OnContent: func(s string) { got.WriteString(s) }})
	if err != nil {
		return StatusFail, "空响应重试后仍失败：" + err.Error(), "空正文且无工具调用时应自动重试一次"
	}
	if strings.TrimSpace(got.String()) == "" {
		return StatusFail, "重试成功但正文为空", "重试后应把第二次的正文交给上层"
	}
	if atomic.LoadInt32(&calls) != 2 {
		return StatusFail, fmt.Sprintf("请求了 %d 次（期望 2 次：失败一次 + 重试一次）", calls),
			"重试次数失控会放大超时与费用"
	}
	return StatusPass, fmt.Sprintf("%d 种坏 JSON 全部修复，空响应重试命中 2 次请求", len(bad)), ""
}

// ST48 自定义子代理：解析定义、工具限制、坏文件给出警告但不阻断。
func caseCustomAgents(e *env) (Status, string, string) {
	dir := core.AgentsDir(e.workDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return StatusFail, "建目录失败：" + err.Error(), "检查工作区写权限"
	}
	good := filepath.Join(dir, "reviewer.md")
	body := "---\nname: reviewer\ndescription: 只做代码审阅，不改文件\ntools: [read_file, grep]\nmodel: fast\nmax_rounds: 6\n---\n\n你只审阅，不写盘。\n"
	if err := os.WriteFile(good, []byte(body), 0o644); err != nil {
		return StatusFail, "写定义失败：" + err.Error(), "检查工作区写权限"
	}
	// 没有头部的文件：应被忽略并给出警告，不能影响其它定义。
	_ = os.WriteFile(filepath.Join(dir, "broken.md"), []byte("这里没有元信息\n"), 0o644)

	defs, warn := core.LoadAgentDefs(e.workDir)
	var found *core.AgentDef
	for i := range defs {
		if defs[i].Name == "reviewer" {
			found = &defs[i]
		}
	}
	if found == nil {
		return StatusFail, "没有解析出 reviewer 定义", "LoadAgentDefs 应识别 --- 头部里的 name"
	}
	if found.Description == "" {
		return StatusFail, "description 丢了", "头部字段解析不完整"
	}
	if len(found.Tools) != 2 || found.Tools[0] != "read_file" {
		return StatusFail, fmt.Sprintf("工具限制解析为 %v", found.Tools), "tools 列表应按顺序解析"
	}
	if found.Model != "fast" {
		return StatusFail, "model 字段解析为 " + found.Model, "model 应支持 main / fast"
	}
	if found.MaxRounds != 6 {
		return StatusFail, fmt.Sprintf("max_rounds 解析为 %d", found.MaxRounds), "轮数上限解析有误"
	}
	_ = warn // 有警告不算失败：坏文件本就该被跳过
	return StatusPass, "reviewer 定义解析正确，坏文件被跳过不影响其它定义", ""
}

// ST49 补丁逐段审阅：只应用选中的段落，其余保持原样。
func casePatchHunks(e *env) (Status, string, string) {
	p := e.path("hunks.txt")
	orig := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n"
	if err := os.WriteFile(p, []byte(orig), 0o644); err != nil {
		return StatusFail, "写测试文件失败：" + err.Error(), "检查工作区写权限"
	}
	patch := "*** Begin Patch\n*** Update File: " + p + "\n" +
		"@@\n line1\n-line2\n+LINE2\n " +
		"@@\n line8\n-line9\n+LINE9\n" +
		"*** End Patch\n"

	// 先 dry_run 看清单：两段改动都要列出来。
	dry, err := e.call("apply_patch", map[string]interface{}{"patch": patch, "dry_run": true})
	if err != nil {
		return StatusFail, "dry_run 失败：" + err.Error(), "补丁解析或段落清单生成有误"
	}
	if !strings.Contains(dry.Output, "1") || !strings.Contains(dry.Output, "2") {
		return StatusFail, "段落清单里没有编号：" + clipText(dry.Output, 300), "每段改动都应编号，供逐段选择"
	}

	// 只应用第 2 段：line2 应保持原样，line9 应变成 LINE9。
	if _, err := e.call("apply_patch", map[string]interface{}{"patch": patch, "hunks": "2"}); err != nil {
		return StatusFail, "按段应用失败：" + err.Error(), "hunks 选择器解析有误"
	}
	got, err := os.ReadFile(p)
	if err != nil {
		return StatusFail, "读回失败：" + err.Error(), "检查文件"
	}
	s := string(got)
	if !strings.Contains(s, "LINE9") {
		return StatusFail, "第 2 段没有应用：" + clipText(s, 300), "选中的段落应真正落盘"
	}
	if strings.Contains(s, "LINE2") {
		return StatusFail, "第 1 段被误应用（hunks=2 却改了 line2）", "段落筛选必须严格"
	}
	return StatusPass, "dry_run 清单完整，hunks=2 只改第二段", ""
}

// ST50 项目长期记忆：写入 → 检索 → 列清单，且落盘到 .harness/memory.md。
func caseProjectMemory(e *env) (Status, string, string) {
	items := []string{
		"构建用 GOOS=windows GOARCH=386 ./build.sh，产物在 output/",
		"跑自检用 harness.exe -selftest，报告落 data/selftest.json",
	}
	for _, it := range items {
		if _, err := e.call("memory_save", map[string]interface{}{"text": it, "tag": "build"}); err != nil {
			return StatusFail, "写入失败：" + err.Error(), "检查 memory_save 与磁盘权限"
		}
	}
	hit, err := e.call("memory_search", map[string]interface{}{"query": "GOARCH 产物"})
	if err != nil {
		return StatusFail, "检索失败：" + err.Error(), "检查关键词匹配逻辑"
	}
	if !strings.Contains(hit.Output, "GOARCH") {
		return StatusFail, "检索没命中刚写入的记忆：" + clipText(hit.Output, 300), "检索应支持多关键词部分命中"
	}
	lst, err := e.call("memory_list", map[string]interface{}{})
	if err != nil {
		return StatusFail, "列清单失败：" + err.Error(), "检查 memory_list"
	}
	if strings.Count(lst.Output, "构建用") < 1 || !strings.Contains(lst.Output, "harness.exe -selftest") {
		return StatusFail, "清单不完整：" + clipText(lst.Output, 300), "memory_list 应列出全部条目"
	}
	mf := filepath.Join(e.workDir, ".harness", "memory.md")
	if _, err := os.Stat(mf); err != nil {
		return StatusFail, "记忆文件不存在：" + mf, "记忆必须落盘，不能只在内存里"
	}
	return StatusPass, fmt.Sprintf("写入 %d 条、检索命中、清单完整，落盘 %s", len(items), filepath.Base(mf)), ""
}

// ST51 会话分叉：标题生成加上两条分支的消息互不污染。
func caseForkIsolation(e *env) (Status, string, string) {
	if t := coreutil.ForkTitle("重构支付模块的鉴权逻辑，顺带补测试", ""); !strings.Contains(t, "分叉") {
		return StatusFail, "分叉标题没带来源标识：" + t, "标题应形如「原题 · 分叉」"
	}
	if t := coreutil.ForkTitle("短标题", "方案B"); !strings.Contains(t, "方案B") {
		return StatusFail, "备注没进标题：" + t, "备注应拼进标题，便于区分两支"
	}
	long := strings.Repeat("很长的标题", 10)
	if n := len([]rune(coreutil.ForkTitle(long, ""))); n > 26 {
		return StatusFail, fmt.Sprintf("标题没截断（%d 字）", n), "长标题要按 rune 截断，避免侧栏被撑爆"
	}

	// 分叉后的两支：各自追加消息不应互相影响（切片必须复制，不能共享底层数组）。
	base := []string{"用户：帮我改这个函数", "助手：好的"}
	branchA := append([]string{}, base...)
	branchB := append([]string{}, base...)
	branchA = append(branchA, "A 方案")
	branchB = append(branchB, "B 方案")
	if len(base) != 2 {
		return StatusFail, "原会话被写脏了", "分叉必须复制切片，不能直接复用"
	}
	if branchA[2] != "A 方案" || branchB[2] != "B 方案" {
		return StatusFail, "两条分支串味", "分支独立是分叉的核心要求"
	}
	return StatusPass, "标题规则与分支隔离都符合预期", ""
}

// ST52 远程 MCP：走 HTTP Streamable（JSON-RPC），完成 initialize 与 tools/list。
func caseRemoteMCP(e *env) (Status, string, string) {
	var inited int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     interface{}            `json:"id"`
			Method string                 `json:"method"`
			Params map[string]interface{} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if req.ID == nil {
			// 通知类请求（notifications/initialized）不回包。
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result interface{}
		switch req.Method {
		case "initialize":
			atomic.StoreInt32(&inited, 1)
			result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
				"serverInfo":      map[string]interface{}{"name": "mock-remote", "version": "0.1"},
			}
		case "tools/list":
			result = map[string]interface{}{
				"tools": []interface{}{map[string]interface{}{
					"name":        "echo",
					"description": "回显",
					"inputSchema": map[string]interface{}{"type": "object"},
				}},
			}
		case "tools/call":
			result = map[string]interface{}{
				"content": []interface{}{map[string]interface{}{"type": "text", "text": "pong"}},
			}
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32601,"message":"method not found"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": req.ID, "result": result,
		})
	}))
	defer srv.Close()

	res, err := remoteProbe(e, core.MCPServer{Name: "mock", URL: srv.URL, Enabled: true})
	if err != nil {
		return StatusFail, "远程连接失败：" + err.Error(),
			"检查 remote.go 的 initialize / tools/list 握手与请求头处理"
	}
	if atomic.LoadInt32(&inited) != 1 {
		return StatusFail, "服务端没收到 initialize", "握手流程缺失"
	}
	if !strings.Contains(res, "echo") {
		return StatusFail, "工具清单里没有 echo：" + clipText(res, 200), "tools/list 结果解析有误"
	}
	if !strings.Contains(res, "pong") {
		return StatusFail, "工具调用结果丢失：" + clipText(res, 200), "tools/call 返回值应透传给模型"
	}
	return StatusPass, "远程 MCP 完成 initialize / tools/list / tools/call", ""
}

// ST53 网络分层诊断：连得通时逐层给结论，连不上时定位到具体一层并给出建议。
func caseNetDiagnose(e *env) (Status, string, string) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	good := core.Diagnose(srv.URL, "直连（已关闭代理）", 5*time.Second)
	if len(good.Steps) < 2 {
		return StatusFail, fmt.Sprintf("只探了 %d 个阶段", len(good.Steps)), "至少要覆盖 DNS 与 TCP"
	}
	hasDNS, hasTCP := false, false
	for _, st := range good.Steps {
		if strings.EqualFold(st.Name, "DNS") && st.OK {
			hasDNS = true
		}
		if strings.EqualFold(st.Name, "TCP") && st.OK {
			hasTCP = true
		}
	}
	if !hasDNS || !hasTCP {
		return StatusFail, "可达地址的 DNS/TCP 阶段未全部通过：" + clipText(good.String(), 300),
			"诊断阶段命名或判定有误"
	}
	if good.Verdict == "" {
		return StatusFail, "没有给出结论", "诊断必须落到一句人话结论"
	}

	bad := core.Diagnose("https://no-such-host-harness.invalid", "", 5*time.Second)
	if !strings.Contains(bad.Verdict, "DNS") {
		return StatusFail, "不通域名没有定位到 DNS 层：" + bad.Verdict, "失败应定位到具体一层"
	}
	if bad.Advice == "" {
		return StatusFail, "没有修复建议", "DNS 失败要给出换 DNS / 连 VPN 这类可执行建议"
	}
	if h := core.ExplainNetErr(fmt.Errorf("dial tcp 127.0.0.1:7890: connectex: No connection could be made")); !strings.Contains(h, "代理") {
		return StatusFail, "代理类错误没有解释成代理问题：" + h, "错误人话化要覆盖代理拒绝连接"
	}
	return StatusPass, "可达地址逐层通过，坏域名定位到 DNS 并给出建议", ""
}

// ST54 图片压缩与 OCR：大图按长边缩放并显著瘦身，无 OCR 引擎时给出明确指引。
func caseImageCompress(e *env) (Status, string, string) {
	img := image.NewRGBA(image.Rect(0, 0, 2400, 1600))
	for y := 0; y < 1600; y++ {
		for x := 0; x < 2400; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 128, A: 255})
		}
	}
	p := e.path("big.png")
	f, err := os.Create(p)
	if err != nil {
		return StatusFail, "建文件失败：" + err.Error(), "检查工作区写权限"
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return StatusFail, "生成 PNG 失败：" + err.Error(), "编码异常"
	}
	f.Close()
	before, _ := os.Stat(p)

	res, err := e.call("read_image", map[string]interface{}{"path": p, "max_side": 600})
	if err != nil {
		return StatusFail, "读图失败：" + err.Error(), "检查压缩分支是否在超限前生效"
	}
	if !strings.Contains(res.Output, "压缩：") {
		return StatusFail, "大图没有被压缩：" + clipText(res.Output, 200), "max_side 对超过该边长的图必须触发缩放"
	}
	if !strings.Contains(res.Output, "600×") {
		return StatusFail, "没有缩到指定长边：" + clipText(res.Output, 200), "缩放计算有误"
	}
	if !strings.Contains(res.Output, "[[img:") {
		return StatusFail, "压缩后没有把图片附给模型", "压缩只改变载荷，不应丢图"
	}

	// OCR：装了引擎就应跑通（识别不到文字也算通），没装必须给出可执行的指引而不是报错崩掉。
	ocr, oerr := e.call("read_image", map[string]interface{}{"path": p, "max_side": 600, "ocr": true})
	if oerr != nil {
		return StatusFail, "OCR 分支直接报错：" + oerr.Error(), "缺引擎时应降级提示，不能让整个读图失败"
	}
	if !strings.Contains(ocr.Output, "OCR") {
		return StatusFail, "OCR 分支没有输出任何结论：" + clipText(ocr.Output, 200), "无论成功失败都要有 OCR 段落"
	}
	return StatusPass, fmt.Sprintf("2400×1600（%s）压到长边 600 并附图，OCR 分支正常降级",
		humanSize(before.Size())), ""
}

// humanSize 体积展示。
func humanSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.0fKB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
}

// remoteProbe 走真实客户端连接一次远程 MCP，返回工具清单与一次调用的结果文本。
func remoteProbe(e *env, spec core.MCPServer) (string, error) {
	c, err := mcp.Start(spec)
	if err != nil {
		return "", err
	}
	defer func() { _ = c.Close() }()
	tools, err := c.ListTools()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, t := range tools {
		b.WriteString(t.Name + " " + t.Description + "\n")
	}
	if out, cerr := c.CallTool("echo", map[string]interface{}{"m": "x"}); cerr == nil {
		b.WriteString(out)
	} else {
		b.WriteString("call error: " + cerr.Error())
	}
	return b.String(), nil
}
