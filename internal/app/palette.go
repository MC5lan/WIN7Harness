package app

import (
	"sort"
	"strconv"
	"strings"

	"win7harness/internal/codeidx"
	"win7harness/internal/commands"
	"win7harness/internal/ui"
	"win7harness/internal/win32"
)

// palCmd 是命令面板中的一条可执行项。
type palCmd struct {
	key  string // 执行标识：以 / 开头走命令，其余走内部动作
	name string
	desc string
}

var palCmds = []palCmd{
	{"/new", "/new", "新建对话"},
	{"/undo", "/undo", "回退上一次文件改动"},
	{"/checkpoints", "/checkpoints", "列出改动清单"},
	{"/diff", "/diff", "查看本次改动"},
	{"/commit", "/commit", "提交变更（自动生成提交信息 + 冲突预检）"},
	{"/gitstatus", "/gitstatus", "git 状态与改动预检"},
	{"/rewind", "/rewind 1", "回滚最近 N 步（git 提交 / 文件快照）"},
	{"/goal", "/goal ", "目标收敛循环：自动拆解、执行、验证到达成"},
	{"/compact", "/compact", "压缩上下文"},
	{"/model", "/model ", "切换本会话模型"},
	{"/model router", "/model router", "模型路由：轻任务走快速模型"},
	{"/recall", "/recall ", "检索历史会话原话"},
	{"/dir", "/dir ", "切换本会话工作区"},
	{"/budget", "/budget", "预算护栏"},
	{"/status", "/status", "运行状态"},
	{"/mcp", "/mcp", "MCP 服务状态"},
	{"/hooks", "/hooks", "工具钩子"},
	{"/index", "/index", "重建代码索引"},
	{"/export", "/export", "导出会话为 Markdown"},
	{"/rename", "/rename", "重命名当前会话"},
	{"/pin", "/pin", "置顶 / 取消置顶会话"},
	{"/crash", "/crash", "查看异常报告"},
	{"/stats", "/stats", "耗时与 token 归因"},
	{"/cost", "/cost", "成本看板与缓存命中率"},
	{"/verify", "/verify on", "改完自动跑构建和测试"},
	{"/mcp add", "/mcp add", "一键安装 MCP 服务（列表选择）"},
	{"/resume", "/resume", "接着上次中断的任务继续"},
	{"/find", "/find ", "跨会话全文检索"},
	{"/open", "/open ", "跳到检索结果"},
	{"/image", "/image", "附加图片（也可直接 Ctrl+V 粘贴截图）"},
	{"/plan", "/plan", "计划模式"},
	{"/fork", "/fork ", "会话分叉：复制当前会话另起一支"},
	{"/perm", "/perm", "审批策略：模式与 allow/deny/ask 规则"},
	{"/agents", "/agents", "自定义子代理清单"},
	{"/netdiag", "/netdiag", "网络分层诊断 DNS/TCP/TLS/HTTP"},
	{"/init", "/init", "生成 AGENTS.md"},
	{"/help", "/help", "命令帮助"},
	{"/remote", "/remote ", "手机远程指挥台"},
	{"/loop", "/loop ", "定时任务"},
	{"/watch", "/watch ", "目录守护"},
	{"/update", "/update ", "版本更新与自更新"},
	{"/perf", "/perf", "运行指标与耗时画像"},
	{"/queue", "/queue", "任务队列"},
	{"/device", "/device ", "设备管理"},
	{"/font", "/font ", "界面字号"},
	{"/theme", "/theme ", "切换主题"},
	{"/compare", "/compare", "并排对比会话"},
	{"/skills", "/skills", "技能清单"},
	{"/skill", "/skill ", "使用技能"},
	{"/eval", "/eval", "评测基线"},
	{"/tools", "/tools", "工具清单"},
	{"/memory", "/memory", "项目记忆"},
	{"/selftest", "/selftest", "端到端自检"},
	{"@settings", "设置", "打开设置页"},
	{"@diag", "自检与日志", "打开自检 / 日志页"},
	{"@theme", "切换主题", "深色 / 浅色外观"},
}

// palMatches 按当前输入过滤命令。
//
// 排序规则：命令名前缀命中 > 命令名包含 > 描述命中 > 工作区符号命中。
// 之前是"命中即平等"，敲 /mo 时 /model 可能排在 /mcp 后面，用户得靠方向键找，
// 面板本来是为了省手，结果多按了两下键，那还不如直接打命令。
func (a *App) palMatches() []palCmd {
	q := strings.ToLower(string(a.palQ))
	type scored struct {
		c     palCmd
		score int
	}
	var hits []scored
	// 用户自定义命令与内置命令一起进面板，palRun 默认分支会走 handleSlash，无需额外分发。
	all := append([]palCmd(nil), palCmds...)
	if wd := a.sessionWorkDir(); wd != "" {
		for _, c := range commands.Scan(wd) {
			all = append(all, palCmd{key: "/" + c.Name, name: "/" + c.Name, desc: c.Desc + "（自定义命令）"})
		}
	}
	for _, c := range all {
		name := strings.ToLower(c.name)
		switch {
		case q == "":
			hits = append(hits, scored{c, 3})
		case strings.HasPrefix(name, q):
			hits = append(hits, scored{c, 4})
		case strings.Contains(name, q):
			hits = append(hits, scored{c, 3})
		case strings.Contains(strings.ToLower(c.desc), q):
			hits = append(hits, scored{c, 2})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })

	out := make([]palCmd, 0, len(hits)+4)
	for _, h := range hits {
		out = append(out, h.c)
	}
	// 输入了具体词就顺带给出工作区里的符号位置：改代码时"跳到那个函数"
	// 比"再打一条 /index"常用得多。
	if len(q) >= 2 && !strings.HasPrefix(q, "/") && !strings.HasPrefix(q, "@") {
		out = append(out, a.palSymbolHits(string(a.palQ))...)
	}
	return out
}

// palSymbolHits 从代码索引里找匹配的符号，作为面板里的追加条目。
// key 用 @sym: 前缀，执行时把定位信息填进输入框，由用户决定发不发。
func (a *App) palSymbolHits(q string) []palCmd {
	ix := codeidx.Cached()
	if ix == nil {
		return nil
	}
	syms := ix.Search(q, 4)
	out := make([]palCmd, 0, len(syms))
	for _, s := range syms {
		name := s.Name
		if s.Recv != "" {
			name = s.Recv + "." + name
		}
		out = append(out, palCmd{
			key:  "@sym:" + s.File + ":" + strconv.Itoa(s.Line),
			name: name,
			desc: s.File + ":" + strconv.Itoa(s.Line) + " · " + s.Kind,
		})
	}
	return out
}

func (a *App) openPalette() {
	a.palOpen = true
	a.palQ = nil
	a.palCur = 0
	a.palIdx = 0
}

// openPaletteWith 打开命令面板并预填内容：顶栏的检索按钮靠它把用户
// 直接送进 /find 的输入态，而不是丢给用户一个需要背的命令名。
func (a *App) openPaletteWith(prefix string) {
	a.palOpen = true
	a.palQ = []rune(prefix)
	a.palCur = len(a.palQ)
	a.palIdx = 0
}

func (a *App) palClose() {
	a.palOpen = false
	a.palQ = nil
	a.palIdx = 0
}

// palRun 执行当前选中项。
func (a *App) palRun() {
	raw := strings.TrimSpace(string(a.palQ))
	ms := a.palMatches()
	if len(ms) == 0 {
		// 面板里直接敲完整命令（含参数）时回车即执行，例如 /find 报错
		if strings.HasPrefix(raw, "/") {
			a.palClose()
			a.handleSlash(raw)
			a.Redraw()
		}
		return
	}
	i := a.palIdx
	if i < 0 || i >= len(ms) {
		i = 0
	}
	c := ms[i]
	a.palClose()
	if strings.HasPrefix(c.key, "@sym:") {
		// 不直接发送：用户可能只是想把位置记下来，填进输入框让他自己按回车
		a.insertInput("请看 " + c.desc)
		a.Redraw()
		return
	}
	switch c.key {
	case "@settings":
		a.page = PageSettings
	case "@diag":
		a.page = PageDiag
		a.diag.tab = 0
		a.runChecksAsync()
	case "@theme":
		a.toggleTheme()
	default:
		a.handleSlash(c.key)
	}
	a.Redraw()
}

// palKey 处理面板打开期间的按键，返回 true 表示已消费。
func (a *App) palKey(vk int32) bool {
	// 再按一次 Ctrl+K 收起面板
	if (vk == 'K' || vk == 'k') && win32.GetKeyState(win32.VK_CONTROL) {
		a.palClose()
		return true
	}
	switch vk {
	case win32.VK_ESCAPE:
		a.palClose()
		return true
	case win32.VK_UP:
		a.palIdx--
		a.palBound()
		return true
	case win32.VK_DOWN:
		a.palIdx++
		a.palBound()
		return true
	case win32.VK_RETURN:
		a.palRun()
		return true
	case win32.VK_BACK:
		if a.palCur > 0 {
			a.palQ = append(a.palQ[:a.palCur-1], a.palQ[a.palCur:]...)
			a.palCur--
			a.palIdx = 0
		}
		return true
	case win32.VK_DELETE:
		if a.palCur < len(a.palQ) {
			a.palQ = append(a.palQ[:a.palCur], a.palQ[a.palCur+1:]...)
			a.palIdx = 0
		}
		return true
	}
	return true
}

func (a *App) palBound() {
	n := len(a.palMatches())
	if n == 0 {
		a.palIdx = 0
		return
	}
	if a.palIdx < 0 {
		a.palIdx = n - 1
	}
	if a.palIdx >= n {
		a.palIdx = 0
	}
}

// palType 处理面板打开期间的字符输入。
func (a *App) palType(ch rune) {
	if ch < 32 {
		return
	}
	rs := []rune(string(ch))
	out := make([]rune, 0, len(a.palQ)+len(rs))
	out = append(out, a.palQ[:a.palCur]...)
	out = append(out, rs...)
	out = append(out, a.palQ[a.palCur:]...)
	a.palQ = out
	a.palCur += len(rs)
	a.palIdx = 0
}

// palClick 处理面板内的鼠标点击。
func (a *App) palClick(x, y int32) {
	id := a.ui.HandleClick(x, y)
	if strings.HasPrefix(id, "pal:") {
		if i, err := strconv.Atoi(id[4:]); err == nil {
			a.palIdx = i
			a.palRun()
		}
		return
	}
	// 点在面板内空白处不关闭，点到面板外才收起
	if !a.palRect.Contains(x, y) {
		a.palClose()
	}
}

func (a *App) paintPalette(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH

	c.FillRect(ui.R(0, 0, W, H), 0x0A0C10)

	ms := a.palMatches()
	if a.palIdx >= len(ms) {
		a.palIdx = 0
	}
	if a.palIdx < 0 {
		a.palIdx = 0
	}

	w := int32(620)
	if w > W-60 {
		w = W - 60
	}
	rows := len(ms)
	if rows > 8 {
		rows = 8
	}
	h := int32(58 + rows*38 + 34)
	x := (W - w) / 2
	y := int32(88)
	if y+h > H-40 {
		y = H - 40 - h
	}
	if y < 20 {
		y = 20
	}
	r := ui.R(x, y, w, h)
	a.palRect = r

	u.Panel(r, th.Surface, th.Border, 12)

	// 输入行
	q := string(a.palQ)
	c.TextFace(r.X+20, r.Y+18, "›", ui.FsMd, 700, th.Accent, ui.FaceUI)
	c.TextFace(r.X+40, r.Y+16, q, ui.FsMd, 400, th.Text, ui.FaceUI)
	cx := r.X + 40 + c.MeasureFace(q, ui.FsMd, 400, ui.FaceUI) + 1
	c.FillRect(ui.R(cx, r.Y+17, 2, 20), th.Accent)
	if q == "" {
		c.TextFace(r.X+42, r.Y+17, "输入命令或功能名…", ui.FsMd, 400, th.TextMute, ui.FaceUI)
	}
	c.HLine(r.X+16, r.Y+52, r.W-32, th.BorderSoft)

	// 列表
	ly := r.Y + 62
	for i, m := range ms {
		if i >= 8 {
			break
		}
		row := ui.R(r.X+10, int32(ly)+int32(i)*38, r.W-20, 34)
		hover := u.Hit("pal:"+strconv.Itoa(i), row)
		if i == a.palIdx || hover {
			c.Round(row, 8, th.CardHover)
		}
		nameCol := th.Text
		if i == a.palIdx {
			nameCol = th.Accent
		}
		c.TextFace(row.X+12, row.Y+7, m.name, ui.FsSm, 700, nameCol, ui.FaceMono)
		c.TextFace(row.X+120, row.Y+8, m.desc, ui.FsXs, 400, th.TextDim, ui.FaceUI)
	}

	// 底部提示
	c.TextFace(r.X+20, r.Bottom()-24, "上下键选择    Enter 执行    Esc 关闭", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	if len(ms) == 0 {
		c.TextFace(r.X+20, r.Y+80, "没有匹配的命令", ui.FsSm, 400, th.TextMute, ui.FaceUI)
	}
}
