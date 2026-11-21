//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"win7harness/internal/agent"
	"win7harness/internal/codeidx"
	"win7harness/internal/core"
	"win7harness/internal/mcp"
	"win7harness/internal/selftest"
	"win7harness/internal/tools"
)

// 本文件由 commands.go 的 handleSlash 拆分而来（T08），仅移动 case body，未改行为。
// 诊断与运行时：/crash /selftest /eval /index /mcp /doctor /status /cost /stats /budget /netdiag /remote /update /perf /device /notices。

// slashCrash 处理 /crash 及别名。
func (a *App) slashCrash(rest string) bool {
	if strings.TrimSpace(rest) == "" {
		a.diag.tab = 4
		a.page = PageDiag
		return true
	}
	a.showCrashReports(rest)
	return true
}

// slashSelftest 处理 /selftest 及别名。
func (a *App) slashSelftest(rest string) bool {
	// 端到端自检：真调工具集跑一遍，结果看诊断页第三栏。
	a.diag.tab = 2
	a.page = PageDiag
	a.runEndToEndAsync()
	a.sysNote("端到端自检已开始，跑完会提示结果。报告目录：" + selftest.DefaultReportDir())
	return true
}

// slashEval 处理 /eval 及别名。
func (a *App) slashEval(rest string) bool {
	// 跳诊断页的评测栏并就地跑：结果按用例一行行列出来，比在聊天里
	// 只报一句"通过率 88.9%"有用得多（哪条挂了、挂在哪，一眼能看到）。
	a.diag.tab = 3
	a.page = PageDiag
	if a.diag.evalRep == nil && !a.diag.evalBusy {
		a.runEvalAsync()
	}
	a.sysNote("评测基线已开始（不调用模型，压工具链 / 索引 / 技能底座）。结果在「自检与诊断 → 评测基线」。")
	return true
}

// slashIndex 处理 /index 及别名。
func (a *App) slashIndex(rest string) bool {
	wd := a.cfg.WorkDir
	if strings.TrimSpace(wd) == "" {
		a.sysNote("尚未设置工作目录，无法建立代码索引。")
		return true
	}
	incremental := strings.EqualFold(rest, "inc") || strings.EqualFold(rest, "增量")
	if incremental {
		a.sysNote("正在增量刷新代码索引…")
	} else {
		a.sysNote("正在重建代码索引…（大仓库会花几秒，期间可以继续聊天）")
	}
	core.SafeGo("app/commands.go:379", func() {
		var ix *codeidx.Index
		if incremental {
			codeidx.Update(wd)
			ix = codeidx.Cached()
		} else {
			ix = codeidx.Refresh(wd)
		}
		if ix == nil {
			ix = codeidx.Build(wd, nil)
		}
		f, syms := ix.Stats()
		msg := fmt.Sprintf("代码索引已重建：%d 个文件 / %d 个符号", f, syms)
		if ix.Truncated {
			msg += fmt.Sprintf("（仓库过大，只索引了前 %d 个文件）", maxFilesNote)
		}
		st := ix.LastBuild
		if st.Incremental {
			msg += fmt.Sprintf("\n增量：复用 %d 个文件、重解析 %d 个（其中刚被改动的 %d 个）、消失 %d 个，耗时 %d ms。",
				st.Reused, st.Parsed, st.Dirty, st.Removed, st.ElapsedMs)
		} else {
			msg += fmt.Sprintf("\n全量：解析 %d 个文件，耗时 %d ms。", st.Parsed, st.ElapsedMs)
		}
		if st.ParseErrs > 0 {
			msg += fmt.Sprintf("\n⚠ %d 个文件解析失败（语法残缺时正常），已跳过：%s", st.ParseErrs, strings.Join(st.SkippedFiles, "、"))
		}
		if st.MemBytes > 0 {
			msg += fmt.Sprintf("\n内存：索引常驻约 %s（符号预算 %s）。",
				core.HumanSize(st.MemBytes), core.HumanSize(st.BudgetBytes))
		}
		msg += "\n@ 后输入符号名可直接定位到定义行，模型也能调用 code_search 工具。"
		select {
		case a.evCh <- agent.Event{Kind: "notice", Text: msg}:
		default:
		}
	})
	return true
}

// slashMCP 处理 /mcp 及别名。
func (a *App) slashMCP(rest string) bool {
	if strings.EqualFold(strings.TrimSpace(rest), "reload") {
		core.SafeGo("app/commands.go:425", func() { a.bootMCP(true) })
		a.sysNote("正在重新连接 MCP 服务…")
		return true
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rest)), "add") {
		a.cmdMCPAdd(strings.TrimSpace(rest)[3:])
		return true
	}
	if strings.TrimSpace(rest) == "" {
		a.page = PageExt
		a.extTab = extTabMCP
		return true
	}
	st := mcp.Statuses()
	if len(st) == 0 {
		cfgHint := "（未配置）"
		if a.cfg != nil && len(a.cfg.MCPServers) > 0 {
			cfgHint = "（已配置但均未连接，可用 /mcp reload 重试）"
		}
		a.sysNote("当前没有已连接的 MCP 服务 " + cfgHint +
			"\n直接说「装个文件系统 MCP」我就带你走向导；也可以输入 /mcp add 从列表里挑。")
		return true
	}
	var b strings.Builder
	b.WriteString("MCP 服务：\n")
	for _, one := range st {
		state := "已连接"
		if !one.Alive {
			state = "连接已断开"
		}
		fmt.Fprintf(&b, "- %s：%d 个工具，%s\n", one.Name, one.Tools, state)
	}
	a.sysNote(strings.TrimRight(b.String(), "\n"))
	return true
}

// slashContext 展示本轮系统提示的分段、工具定义与历史占用，回答“模型看到了什么”。
func (a *App) slashContext(rest string) bool {
	if a.eng == nil {
		return true
	}
	if arg := strings.TrimSpace(rest); arg != "" {
		lower := strings.ToLower(arg)
		if strings.HasPrefix(lower, "cache") {
			val := strings.ToLower(strings.TrimSpace(arg[len("cache"):]))
			switch val {
			case "auto", "off", "anthropic":
			default:
				a.toast("用法：/context cache auto|off|anthropic", true)
				return true
			}
			if a.streaming {
				a.toast("生成中不能改缓存策略，先停止", true)
				return true
			}
			a.cfg.PromptCache = val
			if err := a.cfg.Save(); err != nil {
				a.toast("保存失败："+err.Error(), true)
				return true
			}
			if cv := a.currentConv(); cv != nil {
				a.syncEngineTranscript(cv)
			}
			a.rebuildEngine()
			a.restoreHistory()
			a.sysNote("提示缓存策略已设为 " + val + "。auto 只对已知支持 cache_control 的网关启用；anthropic 强制启用，off 关闭。")
			return true
		}
		if !strings.HasPrefix(lower, "window") {
			a.sysNote("用法：`/context` 查看分段；`/context window 32k` 设置上下文窗口（支持 300k/600k/960k/1m）；`/context cache auto|off|anthropic` 设置缓存策略")
			return true
		}
		val := strings.TrimSpace(arg[len("window"):])
		n, ok := parseWindowSize(val)
		if !ok {
			a.toast("窗口值不合法："+val, true)
			return true
		}
		if a.streaming {
			a.toast("生成中不能改上下文窗口，先停止", true)
			return true
		}
		a.cfg.ContextWindow = n
		if err := a.cfg.Save(); err != nil {
			a.toast("保存失败："+err.Error(), true)
			return true
		}
		if cv := a.currentConv(); cv != nil {
			a.syncEngineTranscript(cv)
		}
		a.rebuildEngine()
		a.restoreHistory()
		if n == 0 {
			a.sysNote("上下文窗口已设为 auto（按 128k 估算，实际以端点为准）。")
		} else {
			a.sysNote(fmt.Sprintf("上下文窗口已设为 %s token，压缩阈值随之下调/上调。", humanTokens(n)))
		}
		return true
	}
	msgs, chars, histTok, window, limit := a.eng.ContextInfo()
	toolN, toolChars := a.eng.ToolDefsStats()
	toolTokens := a.eng.ToolDefsTokens()
	var b strings.Builder
	b.WriteString("**上下文预算**（估算，实际以服务端 usage 为准）\n")
	fmt.Fprintf(&b, "- 模型窗口 %s token · 自动压缩阈值 %s · 当前历史 %d 条 / %s 字符 / 约 %s token\n",
		humanTokens(window), humanTokens(limit), msgs, core.HumanSize(int64(chars)), humanTokens(histTok))
	if strconv.IntSize == 32 {
		b.WriteString("- 当前是 32 位进程：压缩预算硬上限约 60k token，超大窗口只影响模型侧上限，不会按满窗口分配内存。\n")
	}
	sysTok := a.eng.PromptTokens()
	if sysTok+toolTokens > limit {
		fmt.Fprintf(&b, "⚠ 系统提示 + 工具定义约 %s token，已超过压缩预算 %s；历史只能保留很少几条，建议换更大窗口或 `/tools profile core`。\n",
			humanTokens(sysTok+toolTokens), humanTokens(limit))
	}
	fmt.Fprintf(&b, "- 工具定义 %d 个 / %s 字符（profile=%s，计划模式=%v）\n",
		toolN, core.HumanSize(int64(toolChars)), a.eng.EffectiveToolProfile(), a.eng.PlanMode)
	if a.cfg != nil {
		fmt.Fprintf(&b, "- 提示缓存策略：%s（auto 只对已知支持 cache_control 的网关启用）\n", a.cfg.PromptCache)
	}
	if name := a.eng.ActiveSkill(); name != "" {
		fmt.Fprintf(&b, "- 已激活技能：%s（/skill off 取消）\n", name)
	}
	prefixNow, totalNow := a.eng.PromptPrefixStats()
	if totalNow > 0 {
		fmt.Fprintf(&b, "- 与上一轮相比稳定前缀：%d / %d 字符（%.0f%%）\n", prefixNow, totalNow, float64(prefixNow)*100/float64(totalNow))
	}
	fmt.Fprintf(&b, "- 本会话自动压缩 %d 次\n", a.eng.CompactCount())
	b.WriteString("\n**系统提示分段**\n")
	fmt.Fprintf(&b, "| 段 | 字符 | 约 token | 信任 | 稳定 | 截断 |\n|---|---:|---:|---|---|---|\n")
	for _, s := range a.eng.PromptStats() {
		stable := "否"
		if s.Stable {
			stable = "是"
		}
		trunc := ""
		if s.Truncated {
			trunc = "是"
		}
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s |\n",
			s.Title, s.Bytes, humanTokens(s.Tokens), s.Trust, stable, trunc)
	}
	total := a.eng.PromptTotal()
	fmt.Fprintf(&b, "\n系统提示合计 %d 字符；工具定义 %s 字符（约 %s token）。其余预算留给历史消息与模型输出。\n",
		total, core.HumanSize(int64(toolChars)), humanTokens(toolChars/3))
	b.WriteString("\n诊断建议：缓存命中低时先确认 S0-S6（核心/运行环境/用户指令/项目约定/技能/记忆/稳定环境）是否稳定；用 `/context` 对比两次输出即可看出哪段在变。")
	a.sysNote(b.String())
	return true
}

// parseWindowSize 解析 32k / 128000 / 1m / auto。
func parseWindowSize(s string) (int, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "auto", "0", "默认", "":
		return 0, true
	}
	mult := 1
	switch {
	case strings.HasSuffix(s, "k"):
		mult, s = 1000, strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "m"):
		mult, s = 1000000, strings.TrimSuffix(s, "m")
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 || n > 10_000_000 {
		return 0, false
	}
	return n * mult, true
}

// slashDoctor 处理 /doctor 及别名。
func (a *App) slashDoctor(rest string) bool {
	a.cmdDoctor()
	return true
}

// slashStatus 处理 /status 及别名。
func (a *App) slashStatus(rest string) bool {
	if a.eng == nil {
		return true
	}
	n, chars := a.eng.Stats()
	docs := core.LoadProjectDocs(a.eng.WorkDir)
	var names []string
	for _, d := range docs {
		names = append(names, filepath.Base(d.Path))
	}
	docInfo := "未发现（可用 /init 生成）"
	if len(names) > 0 {
		docInfo = strings.Join(names, ", ")
	}
	model := a.convModel()
	if a.sessionModelPinned() {
		model += "（本会话指定）"
	}
	plan := "关闭"
	if a.eng.PlanMode {
		plan = "开启（只读）"
	}
	diffMode := "large（大改动才预览）"
	if a.cfg != nil {
		switch a.cfg.DiffPreview {
		case "off":
			diffMode = "off（不预览）"
		case "always":
			diffMode = "always（每次写盘都预览）"
		}
	}
	gitInfo := "未安装 git"
	if core.GitAvailable() {
		if _, err := os.Stat(filepath.Join(a.eng.WorkDir, ".git")); err == nil {
			gitInfo = "已就绪（/diff、/commit 可用）"
		} else {
			gitInfo = "已安装 git，当前目录不是仓库"
		}
	}
	tokIn, tokOut, turns := a.eng.Usage()
	usageInfo := fmt.Sprintf("输入 %d / 输出 %d token，%d 轮模型调用", tokIn, tokOut, turns)
	if a.cfg != nil && (a.cfg.PriceIn > 0 || a.cfg.PriceOut > 0) {
		cost := float64(tokIn)/1e6*a.cfg.PriceIn + float64(tokOut)/1e6*a.cfg.PriceOut
		usageInfo += fmt.Sprintf("，估算费用 $%.4f（按配置单价）", cost)
	}
	hookCount := 0
	if a.cfg != nil {
		hookCount = len(a.cfg.Hooks)
	}
	mcpInfo := "无"
	if st := mcp.Statuses(); len(st) > 0 {
		parts := make([]string, 0, len(st))
		for _, one := range st {
			state := "OK"
			if !one.Alive {
				state = "断开"
			}
			parts = append(parts, fmt.Sprintf("%s(%d 工具/%s)", one.Name, one.Tools, state))
		}
		mcpInfo = strings.Join(parts, ", ")
	}
	var sb strings.Builder
	sb.WriteString("**运行状态**\n")
	fmt.Fprintf(&sb, "- 模型 `%s`\n", model)
	fmt.Fprintf(&sb, "- 工作目录 `%s`\n", a.eng.WorkDir)
	_, _, _, window, limit := a.eng.ContextInfo()
	fmt.Fprintf(&sb, "- 上下文 %d 条消息 · 约 %d 字符（窗口 %s token，约 %s 自动压缩）\n",
		n, chars, humanTokens(window), humanTokens(limit))
	fmt.Fprintf(&sb, "- 用量 %s\n", usageInfo)
	fmt.Fprintf(&sb, "- 预算 %s\n", a.eng.BudgetLine())
	fmt.Fprintf(&sb, "- 可回滚 %d 步\n", core.SnapshotCount())
	fmt.Fprintf(&sb, "- 项目约定 %s\n", docInfo)
	fmt.Fprintf(&sb, "- 计划模式 %s · 差异预览 %s\n", plan, diffMode)
	fmt.Fprintf(&sb, "- 版本管理 %s\n", gitInfo)
	fmt.Fprintf(&sb, "- 钩子 %d 个 · MCP %s\n", hookCount, mcpInfo)
	fmt.Fprintf(&sb, "- 审批 %s · 目录白名单 %d 个\n", permLine(a.cfg), len(permDirs(a.cfg)))
	defs, _ := core.LoadAgentDefs(a.eng.WorkDir)
	fmt.Fprintf(&sb, "- 项目记忆 %d 条 · 自定义子代理 %d 个\n", len(core.LoadMemory(a.eng.WorkDir)), len(defs))
	if bg := tools.BackgroundCount(); bg > 0 {
		fmt.Fprintf(&sb, "- 后台任务 %d 个在运行（task_list 查看）\n", bg)
	}
	fmt.Fprintf(&sb, "- 图片 %s\n", ocrStatus())
	fmt.Fprintf(&sb, "- 耗时 `%s`", a.eng.BreakLine())
	a.sysNote(sb.String())
	return true
}

// slashCost 处理 /cost 及别名。
func (a *App) slashCost(rest string) bool {
	if strings.TrimSpace(rest) == "" {
		a.page = PageUsage
		a.useTab = useTabCost
		return true
	}
	a.cmdCost()
	return true
}

// slashStats 处理 /stats 及别名。
func (a *App) slashStats(rest string) bool {
	if strings.TrimSpace(rest) == "" {
		a.page = PageUsage
		a.useTab = useTabOverview
		return true
	}
	if a.eng == nil {
		return true
	}
	tokIn, tokOut, turns := a.eng.Usage()
	avgIn, avgOut := 0, 0
	if turns > 0 {
		avgIn, avgOut = tokIn/turns, tokOut/turns
	}
	var b strings.Builder
	b.WriteString("**耗时归因**\n")
	b.WriteString("- 分布 `" + a.eng.BreakLine() + "`\n")
	b.WriteString("- 生成 `" + a.eng.Throughput() + "`\n")
	fmt.Fprintf(&b, "- 调用 `%d 轮 · 平均每轮输入 %d / 输出 %d token`", turns, avgIn, avgOut)
	if stats := a.eng.StatItems(8); len(stats) > 0 {
		b.WriteString("\n\n| 工具 | 次数 | 总耗时 | 平均 | 峰值 | 失败 |\n|---|---|---|---|---|---|\n")
		for _, st := range stats {
			fmt.Fprintf(&b, "| `%s` | %d | %s | %s | %s | %d |\n",
				st.Name, st.Calls, durText(st.Total), durText(st.Avg()), durText(st.Max), st.Fails)
		}
	} else {
		b.WriteString("\n\n本轮还没有工具调用")
	}
	a.sysNote(b.String())
	return true
}

// slashBudget 处理 /budget 及别名。
func (a *App) slashBudget(rest string) bool {
	if a.cfg == nil || a.eng == nil {
		return true
	}
	f := strings.Fields(rest)
	if len(f) == 0 {
		bs := a.eng.Budget()
		limitCost, limitTok := "不限制", "不限制"
		if bs.LimitCost > 0 {
			limitCost = fmt.Sprintf("$%.2f", bs.LimitCost)
		}
		if bs.LimitTokens > 0 {
			limitTok = fmt.Sprintf("%d token", bs.LimitTokens)
		}
		state := "正常"
		if bs.Exceeded {
			state = "已达上限（自动停止调用模型）"
		} else if bs.Warn {
			state = "接近上限"
		}
		a.sysNote(fmt.Sprintf("预算护栏: %s\n本轮已用: %d 轮 · 输入 %d / 输出 %d token · 估算 $%.4f\n上限: 花费 %s · token %s（已用 %0.f%%）\n\n调整: /budget cost 5 · /budget tokens 200000 · /budget off",
			state, bs.Turns, bs.TokIn, bs.TokOut, bs.CostUSD, limitCost, limitTok, bs.UsedRatio*100))
		return true
	}
	switch strings.ToLower(f[0]) {
	case "off":
		a.cfg.MaxCost, a.cfg.BudgetTokens = 0, 0
		a.toast("预算护栏已关闭", false)
	case "cost":
		if len(f) < 2 {
			a.toast("用法：/budget cost 5（美元）", true)
			return true
		}
		var v float64
		if _, err := fmt.Sscanf(f[1], "%f", &v); err != nil || v < 0 {
			a.toast("请输入不小于 0 的数字，0 表示不限制", true)
			return true
		}
		a.cfg.MaxCost = v
		a.toast(fmt.Sprintf("花费上限已设为 $%.2f", v), false)
	case "tokens":
		if len(f) < 2 {
			a.toast("用法：/budget tokens 200000", true)
			return true
		}
		var v int
		if _, err := fmt.Sscanf(f[1], "%d", &v); err != nil || v < 0 {
			a.toast("请输入不小于 0 的整数，0 表示不限制", true)
			return true
		}
		a.cfg.BudgetTokens = v
		a.toast(fmt.Sprintf("token 上限已设为 %d", v), false)
	default:
		a.toast("用法：/budget · /budget cost 5 · /budget tokens 200000 · /budget off", true)
		return true
	}
	if err := a.cfg.Save(); err != nil {
		a.toast("保存失败："+err.Error(), true)
		return true
	}
	a.ui.SetField("set.maxcost", fmt.Sprintf("%g", a.cfg.MaxCost))
	a.ui.SetField("set.budget", fmt.Sprintf("%d", a.cfg.BudgetTokens))
	return true
}

// slashNetDiag 处理 /netdiag 及别名。
func (a *App) slashNetDiag(rest string) bool {
	a.cmdNetDiag(rest)
	return true
}

// slashRemote 处理 /remote 及别名。
func (a *App) slashRemote(rest string) bool {
	a.cmdRemote(rest)
	return true
}

// slashUpdate 处理 /update 及别名。
func (a *App) slashUpdate(rest string) bool {
	// v1.29：apply 走真正的替换+重启，其余仍只是检查。
	if f := strings.Fields(strings.ToLower(rest)); len(f) > 0 {
		tf := f[0]
		switch tf {
		case "apply", "装", "替换":
			a.cmdUpdateApply(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), tf)))
			return true
		case "revert", "rollback", "回退", "回滚", "old", "oldversion":
			a.cmdUpdateRevert(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), tf)))
			return true
		}
	}
	a.cmdUpdate(rest)
	return true
}

// slashPerf 处理 /perf 及别名。
func (a *App) slashPerf(rest string) bool {
	a.cmdPerf(rest)
	return true
}

// slashDevice 处理 /device 及别名。
func (a *App) slashDevice(rest string) bool {
	a.cmdDevice(rest)
	return true
}

// slashNotices 处理 /notices 及别名。
func (a *App) slashNotices(rest string) bool {
	// 升级为页面：看得到、点得动（清空/打开报告），不再只是把文字塞进对话里。
	a.page = PageNotices
	return true
}
