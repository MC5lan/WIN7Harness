package app

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/mcp"
	"win7harness/internal/skills"
	"win7harness/internal/ui"
	"win7harness/internal/ui/kit"
	"win7harness/internal/win32"
)

// 扩展页：已配对设备 / 手机指挥台 / 自定义子代理 / 技能 / MCP / 记忆。
//
// 这五样以前都只有一条斜杠命令，输出一段文字给你看，看完什么也做不了：
// 看到设备列表却踢不掉、看到指挥台地址却没法开关、看到子代理却不知道它带哪些工具。
// 页面要解决的就是"看得见并且点得动"。
//
// 渲染：kit 组件树（与会话/用量/诊断等页一致）；rail 与顶栏沿用 classic。

const (
	extTabDevice = iota
	extTabRemote
	extTabAgents
	extTabSkills
	extTabMCP
	extTabMem
)

func (a *App) paintExt(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH

	c.FillRect(ui.R(0, 0, W, H), th.Bg)
	// 左侧栏：全屏页之间也能直接互跳，不必先退回对话。
	a.paintRail(u, H)
	c.FillRect(ui.R(a.railW, 0, W-a.railW, 56), ui.Blend(th.Bg, th.Surface, 0.6))
	c.HLine(a.railW, 55, W-a.railW, th.BorderSoft)
	DrawLogo(c, a.railW+24, 16, 24, th.Accent, th.Bg)
	c.TextFace(a.railW+56, 18, "扩展", ui.FsLg, 700, th.Text, ui.FaceUI)
	if u.Button("ext.back", "← 返回对话", ui.R(W-140, 14, 116, 30), BtnGhost) {
		a.page = PageChat
	}

	// ---------- kit 内容区 ----------
	ui.SetTokens(a.th.Tokens())
	ktx := a.kit()
	ktx.Painter = kit.NewCanvasAdapter(c)
	ktx.BeginFrame()

	areaW := W - a.railW - 32

	remoteOn := a.v27 != nil && a.v27.rctl != nil && a.v27.rctl.Running()
	remoteLabel := "手机指挥台"
	if remoteOn {
		remoteLabel = "手机指挥台 · 开"
	}
	tabs := []struct {
		id, label string
		idx       int
	}{
		{"ext.tab.d", fmt.Sprintf("设备 · %d", a.deviceCount()), extTabDevice},
		{"ext.tab.r", remoteLabel, extTabRemote},
		{"ext.tab.a", fmt.Sprintf("子代理 · %d", a.cachedAgentCount()), extTabAgents},
		{"ext.tab.s", fmt.Sprintf("技能 · %d", a.cachedSkillCount()), extTabSkills},
		{"ext.tab.m", fmt.Sprintf("MCP · %d", len(mcp.Statuses())), extTabMCP},
		{"ext.tab.mem", fmt.Sprintf("记忆 · %d", a.cachedMemoryCount()), extTabMem},
	}
	tabEls := make([]kit.Element, 0, len(tabs))
	for i, t := range tabs {
		j := i
		id, lbl, idx := t.id, t.label, t.idx
		kind := kit.ButtonSecondary
		if a.extTab == idx {
			kind = kit.ButtonPrimary
		}
		tabEls = append(tabEls, kit.Button(id, lbl, kind, func() { a.extTab = tabs[j].idx }))
	}

	var body kit.Element
	switch a.extTab {
	case extTabDevice:
		body = a.extDevicesBody(ktx, areaW)
	case extTabRemote:
		body = a.extRemoteBody(ktx, areaW)
	case extTabAgents:
		body = a.extAgentsBody(ktx, areaW)
	case extTabSkills:
		body = a.extSkillsBody(ktx, areaW)
	case extTabMCP:
		body = a.extMCPBody(ktx, areaW)
	default:
		body = a.extMemoriesBody(ktx, areaW)
	}

	content := kit.Column(10,
		kit.Row(8, tabEls...),
		body,
	)
	area := kit.R(a.railW+16, 66, W-a.railW-32, H-66-8)
	content.Arrange(ktx, area)
	content.Paint(ktx, ktx.Painter)
}

func (a *App) deviceCount() int {
	if a.v29 == nil || a.v29.devices == nil {
		return 0
	}
	return len(a.v29.devices.List())
}

// extEmpty 空状态（与自动化页一致的左对齐块）。
func extEmpty(title, hint string) kit.Element {
	return kit.Column(8,
		kit.Spacer(),
		kit.Label(title, ui.FsMd, 700, ui.CurrentTokens().TextTertiary),
		kit.Label(hint, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
	)
}

// extListRow 列表行：标题 + 副标题 + 右侧动作按钮，底部一条发丝线。
func extListRow(ktx *kit.Ctx, textW int32, title, sub string, subColor uint32, buttons ...kit.Element) kit.Element {
	tk := ui.CurrentTokens()
	enc := func(s string, size, weight int32, maxW int32) string {
		return kit.TextTrunc(ktx, s, size, weight, maxW, ui.FaceUI)
	}
	col := kit.Column(2,
		kit.Label(enc(title, ui.FsSm, 700, textW), ui.FsSm, 700, tk.TextPrimary),
		kit.LabelFace(enc(sub, ui.FsXs, 400, textW), ui.FsXs, 400, subColor, ui.FaceUI),
	)
	kids := append([]kit.Element{col, kit.Spacer()}, buttons...)
	return kit.Column(6,
		kit.Row(8, kids...),
		kit.Divider(1, tk.BorderHairline, 0),
	)
}

// extOpenFolderButton 目录存在才画"打开目录"按钮；不存在返回空元素占位。
func (a *App) extOpenFolderButton(dir string) kit.Element {
	if dir == "" {
		return kit.Column(0)
	}
	exists, isDir := a.pathStateCached(dir)
	if !exists || !isDir {
		return kit.Column(0)
	}
	return kit.Left(kit.Button("ext.open:"+dir, "打开目录", kit.ButtonGhost, func() {
		win32.ShellOpen(dir)
	}))
}

// extInfoRow "标签 + 等宽值"两列行（值过长省略）。label 为空时整行单列。
func extInfoRow(ktx *kit.Ctx, areaW int32, label, value string) kit.Element {
	tk := ui.CurrentTokens()
	maxW := areaW - 14 - 96 - 8
	val := kit.LabelFace(kit.TextTrunc(ktx, value, ui.FsXs, 400, maxW, ui.FaceMono), ui.FsXs, 400, tk.TextPrimary, ui.FaceMono)
	if label == "" {
		return val
	}
	return kit.Row(8,
		kit.SizedBox(96, 22, kit.Label(label, ui.FsXs, 500, tk.TextSecondary)),
		val,
	)
}

// ---------- 设备 ----------

func (a *App) extDevicesBody(ktx *kit.Ctx, areaW int32) kit.Element {
	tk := ui.CurrentTokens()
	if a.v29 == nil || a.v29.devices == nil {
		return extEmpty("设备表未初始化", "重启程序后再看")
	}
	rows := []kit.Element{}
	if err := a.v29.devices.LoadErr; err != "" {
		rows = append(rows, kit.LabelFace(kit.TextTrunc(ktx, err, ui.FsXs, 400, areaW-8, ui.FaceUI), ui.FsXs, 400, tk.StateDanger, ui.FaceUI))
	}
	list := a.v29.devices.List()
	if len(list) == 0 {
		if len(rows) > 0 {
			return kit.Column(10, rows...)
		}
		return extEmpty("还没有配对过手机",
			"手机指挥台开启后扫码配对，这台手机会出现在这里；丢了手机就在这里吊销它。")
	}
	now := time.Now()
	textW := areaW - 14 - 110
	for _, d := range list {
		d := d
		tone := tk.StateSuccess
		state := "有效"
		if d.Expired(now) {
			tone, state = tk.TextTertiary, "已过期"
		}
		seen := "从未连过"
		if !d.LastSeen.IsZero() {
			seen = "最后使用 " + d.LastSeen.Format("01-02 15:04")
		}
		sub := fmt.Sprintf("%s · 配对于 %s · 到期 %s", seen,
			d.CreatedAt.Format("01-02 15:04"), d.ExpiresAt.Format("01-02 15:04"))
		rows = append(rows, extListRow(ktx, textW, d.Name+"（"+state+"）", sub, tone,
			kit.Button("ext.d.revoke:"+d.ID, "吊销", kit.ButtonSecondary, func() {
				// 按 ID 吊销（同名设备不会误删）；内存 token 同步作废，
				// 被踢设备在进程存活期内无法再发指令。
				if dRV, ok := a.v29.devices.Revoke(d.ID); ok {
					if a.v27 != nil && a.v27.rctl != nil && dRV.Tok != "" {
						a.v27.rctl.ForgetToken(dRV.Tok)
					}
					a.toast("已吊销设备："+dRV.Name+"（它需要重新扫码）", false)
				} else {
					a.toast("吊销失败：设备可能已被其他操作移除", true)
				}
				a.Redraw()
			})))
	}
	rows = append(rows, kit.Left(kit.Button("ext.d.prune", "清理已过期", kit.ButtonGhost, func() {
		n := a.v29.devices.Prune(time.Now())
		a.toast(fmt.Sprintf("已清理 %d 台过期设备", n), false)
		a.Redraw()
	})))
	return kit.Scroll("ext:device", 0, kit.Column(6, rows...))
}

// ---------- 手机指挥台 ----------

func (a *App) extRemoteBody(ktx *kit.Ctx, areaW int32) kit.Element {
	tk := ui.CurrentTokens()
	st := a.v27
	if st == nil || st.rctl == nil {
		return extEmpty("指挥台未初始化", "重启程序后再看")
	}
	running := st.rctl.Running()

	elems := []kit.Element{}
	// 状态行
	state := "已关闭（默认关闭）"
	col := tk.TextTertiary
	if running {
		state, col = "运行中", tk.StateSuccess
	}
	elems = append(elems, kit.Row(8,
		kit.SizedBox(96, 22, kit.Label("状态", ui.FsXs, 500, tk.TextSecondary)),
		kit.Label(state, ui.FsMd, 700, col),
	))

	if running {
		// 用 BaseURL 而不是监听地址：监听地址长这样 http://[::]:8765/，
		// 手机照着输是连不上的。BaseURL 会换成真实的局域网 IP。
		// 地址不再只列一个：多网卡机器上"第一个 IPv4"可能是 VirtualBox 的 192.168.56.1，
		// 手机连不上，而按接口名猜虚拟网卡并不可靠（中文系统里名字是"以太网 2"）。
		// 所以干脆把候选地址全列出来，每个后面配一个二维码按钮——
		// 扫不通就换下一个，比让程序猜要靠谱。
		port := "8765"
		if _, p2, err := net.SplitHostPort(st.rctl.Addr()); err == nil && p2 != "" {
			port = p2
		}
		addrs := a.cachedLocalIPs()
		if len(addrs) == 0 {
			addrs = []string{"127.0.0.1"}
		}
		for i, ip := range addrs {
			label := ""
			if i == 0 {
				label = "地址"
			} else if i == 1 {
				label = "其它地址"
			}
			url := a.remoteScheme() + "://" + net.JoinHostPort(ip, port) + "/"
			u := url
			elems = append(elems, kit.Row(8,
				kit.SizedBox(96, 22, kit.Label(label, ui.FsXs, 500, tk.TextSecondary)),
				kit.LabelFace(kit.TextTrunc(ktx, u, ui.FsXs, 400, areaW-96-110-24, ui.FaceMono), ui.FsXs, 400, tk.TextPrimary, ui.FaceMono),
				kit.Spacer(),
				kit.Button("ext.r.qr:"+ip, "二维码", kit.ButtonSecondary, func() {
					a.showQRFor(u + "?code=" + st.rctl.PairCode())
				}),
			))
		}
		elems = append(elems, extInfoRow(ktx, areaW, "配对码", st.rctl.PairCode()+"（5 分钟有效，一次性）"))
		elems = append(elems, extInfoRow(ktx, areaW, "监听范围", remoteScope(a.cfg.RemoteLAN)))
		elems = append(elems, extInfoRow(ktx, areaW, "待处理指令", fmt.Sprintf("%d 条", st.rctl.Pending())))
		elems = append(elems, extInfoRow(ktx, areaW, "", a.remoteSecurityLine()))
	} else {
		elems = append(elems,
			kit.Label("开启后手机连同一个 Wi-Fi 就能扫码操作这台机器。", ui.FsXs, 400, tk.TextTertiary),
			kit.Label("不做公网穿透；不开 HTTPS 时指令与配对码是明文过网，同网段可被嗅探。", ui.FsXs, 400, tk.TextTertiary),
		)
	}

	// 操作：紧跟在信息块下面，不要沉到页面底部。
	acts := []kit.Element{}
	if !running && a.cfg != nil {
		acts = append(acts, kit.Button("ext.r.on", "开启（局域网）", kit.ButtonPrimary, func() {
			a.cfg.RemoteEnabled = true
			a.cfg.RemoteLAN = true
			_ = a.cfg.Save()
			addr, err := a.startRemote()
			if err != nil {
				a.toast("开启失败："+err.Error(), true)
			} else {
				a.toast("指挥台已开启："+a.remoteURLWithScheme(addr), false)
			}
			a.Redraw()
		}))
		acts = append(acts, kit.Button("ext.r.local", "只本机预览", kit.ButtonGhost, func() {
			a.cfg.RemoteEnabled = true
			a.cfg.RemoteLAN = false
			_ = a.cfg.Save()
			a.stopRemote()
			addr, err := a.startRemote()
			if err != nil {
				a.toast("开启失败："+err.Error(), true)
			} else {
				a.toast("已开启（仅本机）："+a.remoteURLWithScheme(addr), false)
			}
			a.Redraw()
		}))
	}
	if running {
		acts = append(acts, kit.Button("ext.r.off", "关闭指挥台", kit.ButtonDanger, func() {
			a.stopRemote()
			a.toast("指挥台已关闭", false)
			a.Redraw()
		}))
	}
	if a.cfg != nil {
		https := "HTTPS：关（点击切换）"
		if a.cfg.RemoteHTTPS {
			https = "HTTPS：开（点击切换）"
		}
		acts = append(acts, kit.Button("ext.r.https", https, kit.ButtonGhost, func() {
			a.cfg.RemoteHTTPS = !a.cfg.RemoteHTTPS
			_ = a.cfg.Save()
			if running {
				a.stopRemote()
				if _, err := a.startRemote(); err != nil {
					a.toast("重启失败："+err.Error(), true)
				}
			}
			a.Redraw()
		}))
	}
	elems = append(elems, kit.Row(8, acts...))
	return kit.Scroll("ext:remote", 0, kit.Column(10, elems...))
}

// ---------- 自定义子代理 ----------

func (a *App) extAgentsBody(ktx *kit.Ctx, areaW int32) kit.Element {
	tk := ui.CurrentTokens()
	if a.eng == nil {
		return extEmpty("模型还没配置好", "子代理依附于引擎，配置模型后再来看")
	}
	ageDir := filepath.Join(a.eng.WorkDir, ".harness", "agents")
	data := a.uiData()
	defs, warn := data.agents, data.agentsWarn
	rows := []kit.Element{}
	if warn != "" {
		rows = append(rows, kit.LabelFace(kit.TextTrunc(ktx, warn, ui.FsXs, 400, areaW-8, ui.FaceUI), ui.FsXs, 400, tk.StateWarn, ui.FaceUI))
	}
	if len(defs) == 0 {
		rows = append(rows, extEmpty("还没有自定义子代理",
			"在 "+filepath.Join(".harness", "agents")+" 下放一个 .md，头部写 name / description / tools / model 即可。"))
		rows = append(rows, a.extOpenFolderButton(ageDir))
		return kit.Column(10, rows...)
	}
	textW := areaW - 14 - 130
	for _, d := range defs {
		d := d
		sub := d.Description
		if len(d.Tools) > 0 {
			sub += " · 工具：" + joinLimit(d.Tools, 4)
		} else {
			sub += " · 工具：全部"
		}
		if d.Model != "" {
			sub += " · 模型：" + d.Model
		}
		if d.MaxRounds > 0 {
			sub += fmt.Sprintf(" · 轮数：%d", d.MaxRounds)
		}
		// 用系统默认程序打开定义文件：能看、能改、能复制，
		// 比自己弹一个只读窗口有用得多（子代理定义本来就是要编辑的东西）。
		rows = append(rows, extListRow(ktx, textW, d.Name, sub, tk.TextTertiary,
			kit.Button("ext.a.open:"+d.Name, "打开定义", kit.ButtonSecondary, func() {
				if !win32.ShellOpen(d.Path) {
					a.toast("打不开："+d.Path, true)
				}
			})))
	}
	rows = append(rows, a.extOpenFolderButton(ageDir))
	return kit.Scroll("ext:agents", 0, kit.Column(6, rows...))
}

// ---------- 技能 ----------

func (a *App) extSkillsBody(ktx *kit.Ctx, areaW int32) kit.Element {
	tk := ui.CurrentTokens()
	if a.eng == nil {
		return extEmpty("模型还没配置好", "技能依附于会话，配置模型后再来看")
	}
	skDir := filepath.Join(a.eng.WorkDir, ".harness", "skills")
	list := a.uiData().skills
	if len(list) == 0 {
		return kit.Column(10,
			extEmpty("还没有技能",
				"项目技能放 <工作区>/.harness/skills/<名字>/SKILL.md；全局技能放数据目录下的 skills/。"),
			a.extOpenFolderButton(skDir),
		)
	}
	textW := areaW - 14 - 130
	rows := []kit.Element{}
	for _, sk := range list {
		sk := sk
		scope := "项目"
		switch sk.Scope {
		case "global":
			scope = "全局"
		case "builtin":
			scope = "内置"
		}
		sub := scope + " · " + sk.Desc
		if sk.When != "" {
			sub += " · 用于：" + sk.When
		}
		action := kit.Button("ext.s.see:"+sk.Name, "打开文件", kit.ButtonSecondary, func() {
			if !win32.ShellOpen(sk.Path) {
				a.toast("打不开："+sk.Path, true)
			}
		})
		if sk.Scope == "builtin" {
			action = kit.Button("ext.s.copy:"+sk.Name, "复制到项目", kit.ButtonSecondary, func() {
				full, ok := skills.Find(a.convWorkDir(), sk.Name)
				if !ok || full.Body == "" {
					a.toast("读取内置技能失败", true)
					return
				}
				dst := filepath.Join(skills.ProjectDir(a.convWorkDir()), sk.Name, "SKILL.md")
				if _, err := os.Stat(dst); err == nil {
					a.toast("项目里已有同名技能，未覆盖："+dst, true)
					return
				}
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					a.toast("创建目录失败："+err.Error(), true)
					return
				}
				doc := fmt.Sprintf("---\nname: %s\ndescription: %s\nwhen: %s\ncategory: %s\ntags: %s\nversion: %s\n---\n\n%s\n",
					full.Name, full.Desc, full.When, full.Category, full.Tags, full.Version, full.Body)
				if err := core.WriteFileAtomic(dst, []byte(doc), 0o644); err != nil {
					a.toast("复制失败："+err.Error(), true)
					return
				}
				a.uiDataDrop()
				a.toast("已复制到项目技能："+dst, false)
			})
		}
		rows = append(rows, extListRow(ktx, textW, sk.Name, sub, tk.TextTertiary, action))
	}
	return kit.Scroll("ext:skills", 0, kit.Column(6, rows...))
}

// ---------- MCP ----------

func (a *App) extMCPBody(ktx *kit.Ctx, areaW int32) kit.Element {
	tk := ui.CurrentTokens()
	reload := func() {
		core.SafeGo("app/extpage.go:mcpReload", func() { a.bootMCP(true) })
		a.toast("正在重新连接 MCP 服务…", false)
	}
	st := mcp.Statuses()
	if len(st) == 0 {
		hint := "在设置里没有配置 MCP 服务。"
		if a.cfg != nil && len(a.cfg.MCPServers) > 0 {
			hint = fmt.Sprintf("已配置 %d 个服务，但都没有连上；可以点下面的「重新连接」，或检查命令与参数。", len(a.cfg.MCPServers))
		}
		return kit.Column(10,
			extEmpty("当前没有连上的 MCP 服务", hint),
			kit.Left(kit.Button("ext.m.reload", "重新连接", kit.ButtonGhost, reload)),
		)
	}
	textW := areaW - 14 - 8
	rows := []kit.Element{}
	for _, one := range st {
		state, tone := "已连接", tk.StateSuccess
		if !one.Alive {
			state, tone = "连接已断开", tk.StateDanger
		}
		rows = append(rows, extListRow(ktx, textW, one.Name, fmt.Sprintf("%d 个工具 · %s", one.Tools, state), tone))
	}
	rows = append(rows, kit.Left(kit.Button("ext.m.reload2", "重新连接", kit.ButtonGhost, reload)))
	return kit.Scroll("ext:mcp", 0, kit.Column(6, rows...))
}

// ---------- 项目记忆 ----------

// extMemoriesBody 展示 .harness/memory.md 里记录的长期事实。
//
// 项目记忆是"跨会话还记得的事"，出问题（记错、记了过期的结论）时用户得能看见原文，
// 所以这里只做展示 + 打开文件，不在界面里编辑：改记忆就走 /memory 或直接改文件，
// 免得两边各写一份格式。
func (a *App) extMemoriesBody(ktx *kit.Ctx, areaW int32) kit.Element {
	tk := ui.CurrentTokens()
	if a.eng == nil {
		return extEmpty("模型还没配置好", "项目记忆依附于工作区，配置模型后再来看")
	}
	file := core.MemoryFile(a.eng.WorkDir)
	list := a.uiData().memory
	if len(list) == 0 {
		rows := []kit.Element{extEmpty("还没有项目记忆",
			"模型在需要长期记住某事时会自己写进来（比如\"这个项目的测试入口是 make test\"）；"+
				"你也可以直接说「记住：……」让它记一条。下次开新会话会自动带上，不用重复解释背景。")}
		if file != "" {
			rows = append(rows, a.extOpenFolderButton(filepath.Dir(file)))
		}
		return kit.Column(10, rows...)
	}
	textW := areaW - 14 - 8
	rows := []kit.Element{}
	for _, m := range list {
		sub := m.At
		if m.Tag != "" {
			if sub != "" {
				sub = m.Tag + " · " + sub
			} else {
				sub = m.Tag
			}
		}
		rows = append(rows, extListRow(ktx, textW, m.Text, sub, tk.TextTertiary))
	}
	if file != "" {
		rows = append(rows, kit.Row(8, kit.Spacer(),
			kit.Button("ext.mem.open", "打开记忆文件", kit.ButtonGhost, func() {
				if !win32.ShellOpen(file) {
					a.toast("打不开："+file, true)
				}
			})))
	}
	return kit.Scroll("ext:mem", 0, kit.Column(6, rows...))
}

// joinLimit 拼接并限制条数，多了用"等 N 个"收口。
func joinLimit(items []string, n int) string {
	if len(items) <= n {
		out := ""
		for i, s := range items {
			if i > 0 {
				out += "、"
			}
			out += s
		}
		return out
	}
	out := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			out += "、"
		}
		out += items[i]
	}
	return fmt.Sprintf("%s 等 %d 个", out, len(items))
}

// showQRFor 用指定地址生成配对二维码。
// 与 v128 的 showQR 的区别是"用哪个 IP"由调用方决定：多网卡时让用户自己挑，
// 而不是让程序猜一个可能连不上的地址。
func (a *App) showQRFor(url string) {
	if a.v27 == nil || a.v27.rctl == nil || !a.v27.rctl.Running() {
		a.toast("指挥台还没开启", true)
		return
	}
	q, err := core.EncodeQR(url)
	if err != nil {
		a.toast("二维码生成失败："+err.Error(), true)
		return
	}
	a.qr = q
	a.qrURL = url
	a.qrUntil = time.Now().Add(qrPanelLife)
	a.Redraw()
}
