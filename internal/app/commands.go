package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"win7harness/internal/agent"
	"win7harness/internal/codeidx"
	"win7harness/internal/commands"
	"win7harness/internal/core"
	"win7harness/internal/mcp"
	"win7harness/internal/selftest"
	"win7harness/internal/skills"
	"win7harness/internal/tools"
)

// currentConvID 返回当前会话 ID（/undo 清单按会话过滤的依据）。
func currentConvID(a *App) string {
	if cv := a.currentConv(); cv != nil {
		return cv.ID
	}
	return ""
}

// 斜杠命令：对标 Claude Code / Codex 的内置命令，避免把控制指令发给模型。
// 全部在本地执行，不消耗 token。

// maxFilesNote 与 codeidx 的文件上限保持一致，仅用于提示文案。
const maxFilesNote = 3000

const slashHelp = "" +
	helpV127 +
	"**对话**\n" +
	"- `/clear` 新建对话（Ctrl+N）\n" +
	"- `/fork [备注]` 会话分叉：复制当前会话另起一支，两个方案各走一条线互不干扰\n" +
	"- `/compact` 压缩上下文：把早期对话摘要成要点，保留最近若干条\n" +
	"- `/rename 标题` 重命名当前会话\n" +
	"- `/pin` 置顶 / 取消置顶当前会话\n" +
	"- `/export [目录]` 导出会话为 Markdown\n" +
	"\n**文件与改动**\n" +
	"- `/undo` 回滚最近一次文件改动（写入 / 修改 / 删除前均有快照，可连续回退）\n" +
	"- `/checkpoints` 列出本会话改动过的文件（1 = 最新）\n" +
	"- `/rewind N` 一次回退最近 N 步改动（在 git 仓库里优先按提交回滚，失败再退回文件快照）\n" +
	"- `/diff` 查看当前工作目录的 git 改动摘要\n" +
	"- `/commit [说明]` 提交当前全部改动（自动预检暂存区与身份，未填说明按改动类型自动生成）\n" +
	"- `/gitstatus` 查看仓库状态：当前分支、未提交改动、最近提交\n" +
	"- `/init` 在当前目录生成 AGENTS.md 项目约定骨架\n" +
	"- `/plan` 切换计划模式：只读调研，禁止写盘与执行命令\n" +
	"- `/goal 目标` 目标收敛循环：自动拆步骤、逐步执行、整体验证，不通过就再来一轮直到达成\n" +
	"\n**检索**\n" +
	"- `/find 关键词` 跨会话全文检索，结果卡片可直接点击跳转\n" +
	"- `/open 序号` 跳到上一次检索结果的第 n 条\n" +
	"\n**运行时**\n" +
	"- `/status` 模型、工作目录、上下文占用、token 用量与费用估算\n" +
	"- `/doctor` 端点诊断：/models、最短对话、工具调用三步，直接显示上游原始报错\n" +
	"- `/stats` 耗时归因：模型还是工具慢、慢在哪个工具、每秒多少 token\n" +
	"- `/cost` 成本看板：输入/输出/缓存命中率，以及缓存帮你省了多少钱\n" +
	"- `/recall 关键词` 跨会话检索：把很久以前的会话原话翻出来（模型自己也会用 history_search 查）\n" +
	"- `/model router on|off` 模型路由：压缩与只读子代理走便宜模型；`/model fast` 选轻任务模型\n" +
	"- `/tools fold|off` 工具卡折叠：连续多条调用合并成一行，Ctrl+Shift+T 一键切换\n" +
	"- `/verify` 自动验证：改完代码自动跑构建 + 测试，失败把摘要交回模型继续修；`/verify on|off` 切换\n" +
	"- `/worktree new [分支名]` 从 HEAD 开隔离副本，大改不污染主工作区；`/worktree merge` 预检+合并，冲突自动回滚；`/worktree remove` 退出\n" +
	"- `/resume` 上次崩溃或断电中断了任务时，接着中断处继续\n" +
	"- `/budget` 预算护栏；`/budget cost 5` 设花费上限（美元），`/budget tokens 200000` 设 token 上限，`/budget off` 关闭\n" +
	"- `/model` 切换本会话模型：`/model` 列清单，`/model 3` 按序号切，`/model 名称` 按名字切，`/model default` 跟随默认\n" +
	"- `/dir` 切换本会话工作区：`/dir` 查看，`/dir C:\\proj` 指定目录，`/dir pick` 弹目录选择框，`/dir default` 跟随默认\n" +
	"- `/hooks` 查看工具钩子（pre_tool / post_tool / turn_end）\n" +
	"- `/mcp` 查看 MCP 连接状态；`/mcp reload` 重连全部服务；`/mcp add` 一键安装向导\n" +
	"- `/index` 重建代码索引（@ 补全与 code_search 依赖它）；`/index inc` 只增量刷新改动过的文件\n" +
	"- `/skills` 查看可用技能；`/skill 名称` 加载技能正文\n" +
	"- `/eval` 跑评测基线（不依赖模型），报告落到输出目录\n" +
	"- `/crash` 查看异常报告与日志路径\n" +
	"- `/memory` 查看内存档位；`/memory on` 低内存模式（256MB 友好），`/memory off` 回到标准\n" +
	"- `/perm` 审批策略：`/perm auto|ask|plan` 切模式，`/perm add allow run_command:git *` 加规则，`/perm del N` 删规则，`/perm test 工具 目标` 预演判定\n" +
	"- `/agents` 列出自定义子代理（`.harness/agents/*.md`），写好后用 `task agent=名字` 指定\n" +
	"- `/netdiag [地址]` 分层诊断连通性：DNS → TCP → TLS → HTTP，直接告诉你卡在哪一层\n" +
	"\n**多模态**\n" +
	"- `/image` 附加图片：弹选图框，也可 Ctrl+V 粘贴截图、直接把图拖进窗口\n" +
	"\n提示：带图对话最多 4 张，可与文字一起发；项目根目录的 AGENTS.md 会被自动读取并注入系统提示。"

// handleSlash 处理以 / 开头的输入，返回 true 表示已消费该输入。
func (a *App) handleSlash(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return false
	}
	cmd := strings.ToLower(fields[0])
	rest := strings.TrimSpace(text[len(fields[0]):])

	a.input = nil
	a.inputCur = 0
	a.ver++

	switch cmd {
	case "/help", "/?", "/h":
		help := slashHelp + "\n**补充**\n" +
			"- `/selftest` 端到端自检 · `/eval` 评测基线 · `/perf` 运行指标\n" +
			"- `/update` 更新检查与自更新 · `/queue` 任务队列 · `/device` 设备管理\n" +
			"- `/remote` 手机指挥台 · `/loop` 定时任务 · `/watch` 目录守护\n" +
			"- `/notices` 通知中心（页面：通知/自动化足迹/异常报告） · `/font` 字号 · `/theme` 主题 · `/compare` 并列对比\n"
		if custom := customCommandHelp(a.sessionWorkDir()); custom != "" {
			help += "\n**自定义命令**\n" + custom
		}
		a.sysNote(help)

	case "/clear", "/new":
		a.newConv()
		a.toast("已新建对话", false)

	case "/compact":
		if a.eng == nil || a.streaming {
			a.toast("当前正在生成，稍后再试", true)
			return true
		}
		a.statusMsg = "正在压缩上下文…"
		core.SafeGo("app/commands.go:106", func() {
			msg, err := a.eng.Compact(context.Background())
			if err != nil {
				a.evCh <- agent.Event{Kind: "notice", Text: "压缩失败：" + err.Error()}
				return
			}
			a.evCh <- agent.Event{Kind: "notice", Text: msg}
		})

	case "/undo":
		res, err := core.UndoLast()
		if err != nil {
			a.toast(err.Error(), true)
			return true
		}
		if res.Existed {
			a.sysNote("已回滚到上一次改动之前：" + res.Path)
		} else {
			a.sysNote("已撤销本次新建的文件：" + res.Path)
		}

	case "/checkpoints", "/cp", "/changes":
		a.sysNote(core.FormatCheckpoints(core.ListCheckpointsConv(20, currentConvID(a))))

	case "/rewind":
		n := 1
		if len(fields) > 1 {
			v, err := strconv.Atoi(fields[1])
			if err != nil || v <= 0 {
				a.toast("用法：/rewind [步数]，例如 /rewind 3", true)
				return true
			}
			n = v
		}
		// 优先走 git 回滚：能一次退回多个提交，且未提交改动会先 stash 保住
		if a.eng != nil && core.IsGitRepo(a.eng.WorkDir) {
			if msg, err := core.GitRewind(a.eng.WorkDir, n); err == nil {
				a.sysNote(msg)
				return true
			} else if core.GitAvailable() {
				a.sysNote("git 回滚未完成，改用文件快照回退：" + err.Error())
			}
		}
		done, err := core.RewindSteps(n)
		if err != nil {
			a.toast(err.Error(), true)
			if len(done) > 0 {
				a.sysNote(core.FormatCheckpoints(core.ListCheckpointsConv(10, currentConvID(a))))
			}
			return true
		}
		var lines []string
		for _, d := range done {
			if d.Existed {
				lines = append(lines, "已恢复："+d.Path)
			} else {
				lines = append(lines, "已删除（本次新建）："+d.Path)
			}
		}
		a.sysNote(fmt.Sprintf("已回退 %d 步：\n%s", len(done), strings.Join(lines, "\n")))

	case "/init":
		if a.eng == nil {
			return true
		}
		wd := a.eng.WorkDir
		p := filepath.Join(wd, "AGENTS.md")
		if _, err := os.Stat(p); err == nil {
			a.sysNote("已存在 AGENTS.md，未覆盖。可直接编辑该文件补充项目约定。")
			return true
		}
		if err := core.WriteFileAtomic(p, []byte(core.ProjectDocSkeleton(wd)), 0o644); err != nil {
			a.toast("写入失败："+err.Error(), true)
			return true
		}
		a.sysNote("已生成项目约定文件：\n" + p + "\n\n补充构建命令、代码风格等约定后，之后每轮对话会自动加载。")

	case "/plan":
		if a.eng == nil {
			return true
		}
		a.eng.PlanMode = !a.eng.PlanMode
		if a.eng.PlanMode {
			a.sysNote("已进入计划模式：我只做调研与方案设计，不写文件、不执行命令。\n方案确认后输入 /plan 退出并开始实施。")
		} else {
			a.sysNote("已退出计划模式：可以正常读写文件与执行命令。")
		}

	case "/diff":
		if a.eng == nil {
			return true
		}
		wd := a.eng.WorkDir
		if _, err := os.Stat(filepath.Join(wd, ".git")); err != nil {
			a.sysNote("当前工作目录不是 git 仓库：\n" + wd + "\n如需版本管理可在该目录执行 git init。")
			return true
		}
		short, err := core.GitRun(wd, "status", "--short")
		if err != nil {
			a.sysNote("git status 失败：" + err.Error())
			return true
		}
		stat, err := core.GitRun(wd, "diff", "--stat")
		if err != nil {
			a.sysNote("git diff 失败：" + err.Error())
			return true
		}
		if strings.TrimSpace(short) == "" {
			a.sysNote("工作区干净，没有未提交的改动。")
			return true
		}
		a.sysNote("未提交改动：\n" + strings.TrimRight(short, "\n") + "\n\n改动统计：\n" + strings.TrimRight(stat, "\n") + "\n\n提示：/commit 提交，或在对话里让我 review。")

	case "/commit":
		if a.eng == nil {
			return true
		}
		wd := a.eng.WorkDir
		if !core.IsGitRepo(wd) {
			a.sysNote("当前工作目录不是 git 仓库，无法提交：\n" + wd +
				"\n\n想做版本管理可先执行：git init && git add -A && git commit -m init")
			return true
		}
		msg, err := core.CommitAll(wd, strings.TrimSpace(rest))
		if err != nil {
			a.sysNote("提交未完成：" + err.Error())
			return true
		}
		a.sysNote(msg)

	case "/gitstatus", "/gs":
		if a.eng == nil {
			return true
		}
		wd := a.eng.WorkDir
		if !core.IsGitRepo(wd) {
			a.sysNote("不是 git 仓库：" + wd)
			return true
		}
		var b strings.Builder
		if st := core.GitState(wd); st != "" {
			b.WriteString(st + "\n")
		}
		changes := core.GitChanges(wd)
		if len(changes) == 0 {
			b.WriteString("工作区干净。\n")
		} else {
			fmt.Fprintf(&b, "共 %d 处改动：\n", len(changes))
			for _, c := range changes {
				fmt.Fprintf(&b, "  [%s] %s\n", c.Stage(), c.Path)
			}
			if block, notes := core.GitPreflight(wd); block != "" {
				b.WriteString("\n预检未通过：" + block + "\n")
			} else if len(notes) > 0 {
				b.WriteString("\n预检提示：\n")
				for _, n := range notes {
					b.WriteString("  - " + n + "\n")
				}
			}
		}
		a.sysNote(b.String())

	case "/goal":
		a.cmdGoal(rest)

	case "/image":
		a.pickImages()
		return true

	case "/rename":
		if rest == "" {
			// 不带标题就进入就地改名，光标直接落在标题上
			a.startRename(a.active)
			break
		}
		a.renameConv(a.active, rest)
		a.toast("已重命名", false)

	case "/pin":
		a.togglePin(a.active)

	case "/crash":
		if strings.TrimSpace(rest) == "" {
			a.diag.tab = 4
			a.page = PageDiag
			break
		}
		a.showCrashReports(rest)

	case "/selftest", "/e2e":
		// 端到端自检：真调工具集跑一遍，结果看诊断页第三栏。
		a.diag.tab = 2
		a.page = PageDiag
		a.runEndToEndAsync()
		a.sysNote("端到端自检已开始，跑完会提示结果。报告目录：" + selftest.DefaultReportDir())

	case "/export":
		conv := a.currentConv()
		if conv == nil || len(conv.Msgs) == 0 {
			a.sysNote("当前会话还没有内容，没什么可导出的。")
			break
		}
		target := strings.TrimSpace(rest)
		p, err := a.ExportConv(conv, target)
		if err != nil {
			a.sysNote("导出失败：" + err.Error())
			break
		}
		a.sysNote("会话已导出为 Markdown：\n" + p + "\n可以直接发给同事，或用编辑器改。" +
			"\n想指定目录：/export D:\\笔记")
		revealPath(p, true)

	case "/skills":
		// 无参打开扩展页：清单做成列表比在聊天里贴一段更能用（能打开正文文件）。
		// 带参数（/skills list）仍走文字版，方便脚本化与日志留痕。
		if strings.TrimSpace(rest) == "" {
			a.page = PageExt
			a.extTab = extTabSkills
			return true
		}
		list := skills.Scan(a.cfg.WorkDir)
		if len(list) == 0 {
			a.sysNote(fmt.Sprintf("暂无技能。把流程文档放到下面任一位置即可被自动发现：\n%s\\<技能名>\\SKILL.md\n%s\\<技能名>.md",
				skills.ProjectDir(a.cfg.WorkDir), skills.GlobalDir()))
			break
		}
		var b strings.Builder
		fmt.Fprintf(&b, "可用技能 %d 个：\n", len(list))
		for _, sk := range list {
			scope := "项目"
			if sk.Scope == "global" {
				scope = "全局"
			}
			fmt.Fprintf(&b, "- %s（%s）%s\n", sk.Name, scope, sk.Desc)
		}
		b.WriteString("用 /skill 名称 加载正文，模型侧会自动用 skill_use 加载。")
		a.sysNote(b.String())

	case "/commands", "/cmd":
		list := commands.Scan(a.sessionWorkDir())
		var b strings.Builder
		if len(list) == 0 {
			fmt.Fprintf(&b, "暂无自定义命令。把 Markdown 放到下面任一位置即可被 /命令名 调用：\n%s\\<名字>.md\n%s\\<名字>.md\n",
				commands.ProjectDir(a.sessionWorkDir()), commands.GlobalDir())
		} else {
			fmt.Fprintf(&b, "自定义命令 %d 个（展开到输入框，可编辑后再发送）：\n", len(list))
			for _, c := range list {
				scope := "项目"
				if c.Scope == "global" {
					scope = "全局"
				}
				fmt.Fprintf(&b, "- /%s（%s）%s\n", c.Name, scope, c.Desc)
			}
		}
		a.sysNote(b.String())

	case "/skill":
		name := strings.TrimSpace(rest)
		if name == "" {
			a.sysNote("用法：/skill 技能名（先用 /skills 看清单）")
			break
		}
		sk, ok := skills.Find(a.cfg.WorkDir, name)
		if !ok {
			a.sysNote("没有名为“" + name + "”的技能，先用 /skills 查看清单。")
			break
		}
		a.sysNote(fmt.Sprintf("技能 %s 已就绪（%s）。直接说要做的事，我会按该流程执行。", sk.Name, sk.Path))

	case "/eval":
		// 跳诊断页的评测栏并就地跑：结果按用例一行行列出来，比在聊天里
		// 只报一句"通过率 88.9%"有用得多（哪条挂了、挂在哪，一眼能看到）。
		a.diag.tab = 3
		a.page = PageDiag
		if a.diag.evalRep == nil && !a.diag.evalBusy {
			a.runEvalAsync()
		}
		a.sysNote("评测基线已开始（不调用模型，压工具链 / 索引 / 技能底座）。结果在「自检与诊断 → 评测基线」。")

	case "/index":
		wd := a.cfg.WorkDir
		if strings.TrimSpace(wd) == "" {
			a.sysNote("尚未设置工作目录，无法建立代码索引。")
			break
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

	case "/hooks":
		lines := tools.HooksSummary()
		if len(lines) == 0 {
			a.sysNote("尚未配置钩子。可在 config.json 的 hooks 数组里添加，例如：\n" +
				`  {"event":"post_tool","match":"write_file,edit_file","command":"gofmt -w %f"}` +
				"\n支持事件：pre_tool（非零退出码可阻止工具执行）、post_tool、turn_end；match 支持 * 通配与逗号分隔。")
		} else {
			a.sysNote("已配置钩子（按字母序）：\n" + strings.Join(lines, "\n"))
		}

	case "/mcp":
		if strings.EqualFold(strings.TrimSpace(rest), "reload") {
			core.SafeGo("app/commands.go:425", func() { a.bootMCP(true) })
			a.sysNote("正在重新连接 MCP 服务…")
			break
		}
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rest)), "add") {
			a.cmdMCPAdd(strings.TrimSpace(rest)[3:])
			break
		}
		if strings.TrimSpace(rest) == "" {
			a.page = PageExt
			a.extTab = extTabMCP
			break
		}
		st := mcp.Statuses()
		if len(st) == 0 {
			cfgHint := "（未配置）"
			if a.cfg != nil && len(a.cfg.MCPServers) > 0 {
				cfgHint = "（已配置但均未连接，可用 /mcp reload 重试）"
			}
			a.sysNote("当前没有已连接的 MCP 服务 " + cfgHint +
				"\n直接说「装个文件系统 MCP」我就带你走向导；也可以输入 /mcp add 从列表里挑。")
			break
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

	case "/doctor", "/诊断":
		a.cmdDoctor()
		return true

	case "/status":
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
		fmt.Fprintf(&sb, "- 上下文 %d 条消息 · 约 %d 字符（超 90000 自动压缩）\n", n, chars)
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

	case "/find":
		q := strings.TrimSpace(rest)
		if q == "" {
			a.sysNote("用法：`/find 关键词 [关键词2]`\n\n在全部会话里检索正文与工具输出，多个词用空格分隔（需同时出现）。")
			break
		}
		hits := a.FindAll(q, 30)
		a.findHits = hits
		if len(hits) == 0 {
			a.sysNote("没有找到包含「" + q + "」的内容（共检索 " + fmt.Sprint(len(a.convs)) + " 个会话）。")
			break
		}
		views := make([]HitView, 0, len(hits))
		for _, h := range hits {
			title := h.Title
			if title == "" {
				title = "新对话"
			}
			src := "助手答"
			switch h.Role {
			case "title":
				src = "标题"
			case "user":
				src = "你问"
			}
			when := ""
			if !h.At.IsZero() {
				when = h.At.Format("01-02 15:04")
			}
			snippet := strings.Join(strings.Fields(h.Snippet), " ")
			views = append(views, HitView{
				Title: title, Src: src, When: when, Snippet: snippet,
				ConvIdx: h.ConvIdx, MsgIdx: h.MsgIdx,
			})
		}
		a.sysNoteHits(fmt.Sprintf("**「%s」命中 %d 处** · 检索了 %d 个会话", q, len(hits), len(a.convs)), views)

	case "/tools":
		arg := strings.ToLower(strings.TrimSpace(rest))
		switch arg {
		case "":
			state := "开启（连续 3 条以上已结束的工具调用合并成一行）"
			if a.cfg.NoFoldTools {
				state = "关闭（每条工具单独一行）"
			}
			a.sysNote("**工具卡折叠** 当前：" + state +
				"\n\n`/tools fold` 开启 · `/tools off` 关闭 · 快捷键 Ctrl+Shift+T\n" +
				"折叠只影响显示，工具输出仍在上下文里，模型照常能看到。")
		case "fold", "on", "开", "开启":
			a.cfg.NoFoldTools = false
			a.foldOpen = nil
			_ = a.cfg.Save()
			a.layout()
			a.sysNote("工具卡已按组折叠。单组点一下展开，再点收起。")
		case "off", "unfold", "关", "关闭", "展开":
			a.cfg.NoFoldTools = true
			a.foldOpen = nil
			_ = a.cfg.Save()
			a.layout()
			a.sysNote("工具卡已改为逐条显示。")
		default:
			a.toast("用法：/tools [fold|off]", true)
		}

	case "/recall":
		q := strings.TrimSpace(rest)
		if q == "" {
			a.sysNote("用法：`/recall 关键词`\n\n检索已保存的全部会话（含很久以前的），按会话时间列出命中原话。\n" +
				"想按会话内位置跳转用 `/find`；想让模型自己去翻历史，直接说『查一下上次怎么解决的』，它会调用 history_search。")
			break
		}
		hits, scanned, err := tools.SearchHistory(core.SessionsDir(), q, 12, 0, time.Now())
		if err != nil {
			a.toast("检索失败："+err.Error(), true)
			break
		}
		if len(hits) == 0 {
			a.sysNote(fmt.Sprintf("在 %d 个已保存会话里没有找到「%s」。\n可以换更短的关键词，或先用 `/find` 在内存中的会话里再试一次。", scanned, q))
			break
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "**「%s」命中 %d 处**（扫描 %d 个会话，按时间倒序）\n", q, len(hits), scanned)
		for i, h := range hits {
			title := h.Title
			if title == "" {
				title = "新对话"
			}
			who := "助手答"
			switch h.Role {
			case "user":
				who = "你问"
			case "title":
				who = "标题"
			}
			if strings.HasPrefix(h.Role, "工具·") {
				who = h.Role
			}
			fmt.Fprintf(&sb, "\n%d. 《%s》%s · %s\n   %s\n", i+1, title, h.At.Format("01-02 15:04"), who, h.Around)
		}
		a.sysNote(sb.String())

	case "/open", "/go":
		n := 0
		for _, r := range strings.TrimSpace(rest) {
			if r < '0' || r > '9' {
				n = 0
				break
			}
			n = n*10 + int(r-'0')
		}
		if n == 0 {
			a.sysNote("用法：/open 序号（序号来自 /find 的结果列表）")
			break
		}
		a.OpenFindHit(n)

	case "/verify":
		a.cmdVerify(rest)

	case "/worktree":
		a.cmdWorktree(rest)

	case "/cost":
		if strings.TrimSpace(rest) == "" {
			a.page = PageUsage
			a.useTab = useTabCost
			break
		}
		a.cmdCost()

	case "/resume":
		a.resumeLast()

	case "/stats":
		if strings.TrimSpace(rest) == "" {
			a.page = PageUsage
			a.useTab = useTabOverview
			break
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

	case "/budget":
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

	case "/memory", "/mem":
		f := strings.Fields(strings.ToLower(rest))
		if len(f) > 0 {
			on := f[0] == "on" || f[0] == "low" || f[0] == "1"
			off := f[0] == "off" || f[0] == "normal" || f[0] == "0"
			if !on && !off {
				a.toast("用法：/memory · /memory on · /memory off", true)
				break
			}
			core.SetLowMemory(on)
			if a.cfg != nil {
				a.cfg.LowMemory = on
				if err := a.cfg.Save(); err != nil {
					a.toast("保存失败："+err.Error(), true)
				}
			}
			tip := "低内存模式已开启，新的调用立即按新阈值执行；代码索引规模要 /index 重建才会回落。"
			if !on {
				tip = "已回到标准档位，新的调用立即按标准阈值执行；索引上限需 /index 重建才会放开。"
			}
			a.sysNote(tip + "\n\n" + core.MemProfile().Describe())
			break
		}
		a.sysNote(fmt.Sprintf("内存档位说明（当前：%s）\n%s\n\n切换：/memory on 省内存 · /memory off 回到标准",
			core.MemProfile().ModeName(), core.MemProfile().Describe()))

	case "/model":
		a.cmdModel(rest)

	case "/fork", "/branch":
		a.cmdFork(rest)

	case "/perm", "/permissions", "/policy":
		a.cmdPerm(rest)

	case "/agents", "/subagents":
		a.cmdAgents(rest)

	case "/netdiag", "/diag-net":
		a.cmdNetDiag(rest)

	case "/dir", "/workdir", "/workspace":
		a.cmdWorkDir(rest)

	case "/remote", "/phone", "/手机":
		a.cmdRemote(rest)

	case "/loop", "/schedule", "/定时":
		a.cmdLoop(rest)

	case "/watch", "/守护":
		a.cmdWatch(rest)

	case "/update", "/升级":
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

	case "/perf", "/性能":
		a.cmdPerf(rest)

	case "/queue", "/队列":
		a.cmdQueue(rest)

	case "/device", "/devices", "/设备":
		a.cmdDevice(rest)

	case "/font", "/字号":
		a.cmdFont(rest)

	case "/theme", "/主题":
		a.cmdTheme(rest)

	case "/compare", "/对比":
		a.cmdCompare(rest)

	case "/notices", "/通知":
		// 升级为页面：看得到、点得动（清空/打开报告），不再只是把文字塞进对话里。
		a.page = PageNotices

	default:
		// 内置命令优先；未命中时再查用户自定义命令。
		if c, ok := commands.Find(a.sessionWorkDir(), cmd); ok {
			a.insertInput(c.Expand(rest))
			a.ui.SetFocus("input")
			a.toast("已展开自定义命令 /"+c.Name, false)
			return true
		}
		a.toast("未知命令 "+cmd+"，输入 /help 查看", true)
	}
	return true
}

// customCommandHelp 生成 /help 里的动态"自定义命令"段。
func customCommandHelp(workDir string) string {
	list := commands.Scan(workDir)
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	for _, c := range list {
		b.WriteString("- `/" + c.Name + "` " + c.Desc + "（" + c.Scope + "）\n")
	}
	return b.String()
}

// sysNote 追加一条本地系统提示消息（不经过模型）。
// showCrashReports 列出最近的异常报告，/crash open 打开目录。
func (a *App) showCrashReports(rest string) {
	list := core.ListCrashReports(5)
	if strings.EqualFold(strings.TrimSpace(rest), "open") {
		revealPath(core.CrashDir(), false)
		return
	}
	if len(list) == 0 {
		a.sysNote("没有异常报告，说明最近没崩过。\r\n日志位置：" + core.LogPath())
		return
	}
	var b strings.Builder
	b.WriteString("最近的异常报告（越靠上越新）：\r\n")
	for _, p := range list {
		b.WriteString("· " + p + "\r\n")
	}
	b.WriteString("\r\n打开目录：/crash open\r\n日志：" + core.LogPath())
	a.sysNote(b.String())
}

// sysNoteHits 追加一条带检索结果卡的系统消息。
func (a *App) sysNoteHits(text string, hits []HitView) {
	a.msgs = append(a.msgs, Message{Role: "assistant", Text: text, Hits: hits, At: time.Now()})
	a.curAsst = -1
	a.ver++
	a.autoScroll = true
	a.saveConv()
	a.scrollToBottom()
}

func (a *App) sysNote(text string) {
	a.msgs = append(a.msgs, Message{Role: "assistant", Text: text, At: time.Now()})
	a.curAsst = -1
	a.ver++
	a.autoScroll = true
	a.saveConv()
	a.scrollToBottom()
}
