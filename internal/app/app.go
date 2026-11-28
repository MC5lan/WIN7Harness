//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"win7harness/internal/agent"
	"win7harness/internal/bootstrap"
	"win7harness/internal/codeidx"
	"win7harness/internal/core"
	"win7harness/internal/llm"
	"win7harness/internal/skills"
	"win7harness/internal/ui"
)

// isKitPage 报告页面是否用 kit 组件树绘制（共用同一套鼠标/键盘/滚轮转发路径）。
func isKitPage(p Page) bool {
	return p == PageSessions || p == PageWorkspace || p == PageCosts || p == PageCompare || p == PageAuto || p == PageUsage || p == PageDiag || p == PageExt || p == PageNotices
}

// kitTopH 是 kit 页顶栏的下边界：顶栏上的 logo、返回按钮以及左侧 rail
// 仍由 classic UI 绘制并命中，只有它下面的正文区才归 kit。
const kitTopH = 60

// kitOwnsPoint 报告坐标是否落在 kit 页正文区。把 rail 与顶栏留给 classic，
// 否则「← 返回对话」这类 classic 按钮会被 kit 的鼠标路由整个吞掉。
func (a *App) kitOwnsPoint(x, y int32) bool {
	return isKitPage(a.page) && x >= a.railW && y >= kitTopH
}

// New 创建应用。
func New(cfg *core.Config, w *ui.Window) *App {
	a := &App{
		w:             w,
		editingID:     -1,
		cfg:           cfg,
		th:            ui.ResolveTheme(cfg.Theme),
		active:        0,
		curAsst:       -1,
		jumpMsg:       -1,
		hlMsg:         -1,
		showSide:      true,
		showInsp:      cfg.ShowInsp,
		autoScroll:    true,
		evCh:          make(chan agent.Event, 4096),
		confirmCh:     make(chan confirmReq, 16),
		diffCh:        make(chan diffReq, 16),
		planCh:        make(chan planReq, 16),
		testCh:        make(chan testResult, 4),
		modelsCh:      make(chan modelListResult, 4),
		convLoadCh:    make(chan convLoadResult, 4),
		doctorCh:      make(chan testResult, 2),
		hydrateCh:     make(chan *Conv, 8),
		compScanDone:  make(chan struct{}, 2),
		promptHistory: core.LoadPromptHistory(),
		lastTick:      time.Now(),
		perf:          newPerfState(),
		pdata:         newPageDataState(),
		ws:            newWSState(),
		sessIdx:       newSessIndexState(),
		pathCache:     map[string]pathCacheEntry{},
		modelsCache:   map[string][]string{},
	}
	a.showThink = cfg.ShowThinking
	a.runMode = "标准"
	if core.PortableMode() {
		a.runMode = "便携"
	} else if core.InstallMode() == "standard-all" {
		a.runMode = "所有用户"
	}
	a.inputSel = -1
	// 会话落盘队列要在任何 load/save 之前就绪：New 里马上会 loadAllConvs，
	// 引导页/退出路径也可能立刻触发保存。
	a.saver = newConvSaveQueue()
	core.SafeGo("app/store.go:saver", func() { a.saver.loop(a) })
	// 面板宽度取自上次拖拽结果，缺省回落到内置默认值。
	a.sideWUser, a.inspWUser = defSideW, defInspW
	if cfg.SideW > 0 {
		a.sideWUser = int32(cfg.SideW)
	}
	if cfg.InspW > 0 {
		a.inspWUser = int32(cfg.InspW)
	}
	// 会话先于页面就绪：引导页完成后直接落回上次的对话。
	a.loadAllConvs()
	// 无论是否已完成引导都载入当前会话的消息。以前只在 onboarded=true 时载入，
	// 引导页下 a.msgs 为空，退出时的 saveConv 会把最新一条会话写成空壳
	// （配置损坏回退默认时尤其容易触发，等于静默丢历史）。
	a.loadConv()
	if !cfg.Onboarded {
		a.page = PageOnboard
	} else {
		a.page = PageChat
	}
	// 上一次的轮次没有正常收尾 = 崩溃或断电，给出续做提示。
	a.jr = core.NewJournal(core.SessionsDir())
	a.loadRecovery()
	// 上次异常退出时把线索直接放进对话里，别让人去翻日志。
	if w := core.StartupWarning(); w != "" {
		a.noteStartupWarning(w)
		core.SetStartupWarning("")
	}
	a.rebuildEngine()
	a.initV127()
	a.initV128()
	a.initV129()
	// 页面数据（技能/记忆/崩溃报告/网卡/git 身份）改由后台刷新：
	// 绘制线程永不为了一个页签去 ReadDir 或起 git 子进程。
	a.startPageDataLoop()
	// 工具全文仓库只保留有限数量，启动时后台清理一次。
	core.SafeGo("app:prune-artifacts", func() { core.PruneArtifacts(30*24*time.Hour, 800) })
	// 首次启动投放一个示例技能，用户 /skills 就能看到模板；已有技能时不打扰。
	skills.EnsureSample(cfg.WorkDir)
	a.uiDataDrop()
	// 会话中心索引在后台预热：用户点开"会话中心"时不用等第一次解析。
	core.SafeGo("app/pages_kit.go:preloadSessions", func() { a.triggerSessionIndex() })
	// MCP 服务在后台连接，失败不影响主界面
	a.bootMCP(false)
	return a
}

// rebuildEngine 按当前配置重建模型客户端与引擎。
// rebuildEngine 按当前会话解析出的端点/模型/工作区重建引擎。
// 全局设置只提供默认值，会话钉住的值优先——这样在 A 会话切到本地小模型，
// 不会把 B 会话也带走。
func (a *App) rebuildEngine() {
	p := a.convProvider()
	e, model, warn, err := bootstrap.NewEngineFor(a.cfg, p, a.convModel(), a.convWorkDir(), bootstrap.Options{
		Confirm:     a.confirm,
		ConfirmDiff: a.confirmDiff,
		PlanApprove: func(title, plan string, steps int) (string, string) {
			d := a.confirmPlan(title, plan, steps)
			return d.Action, d.Text
		},
		Emit: func(ev agent.Event) {
			select {
			case a.evCh <- ev:
			default:
				// 极端拥塞时阻塞，避免丢内容
				a.evCh <- ev
			}
		},
	})
	if warn != "" {
		a.toast(warn, true)
	}
	if err != nil {
		// 地址还没填（首次引导阶段）：留空引擎，界面照常显示设置页。
		a.eng = nil
		return
	}
	if e != nil && e.Client != nil {
		// OpenCode Go 用 x-opencode-session 做路由与提示缓存；
		// 以会话 ID 为稳定值，切会话时随 rebuildEngine 更新。
		if cv := a.currentConv(); cv != nil && cv.ID != "" {
			e.Client.SessionID = "win7h-" + cv.ID
		}
	}
	a.eng = e
	a.eng.Model = model
	// 路由与子任务预算每次都从配置同步：改了设置不用重启就生效
	a.applyRouter()

	// 代码索引：先吃磁盘缓存做到立刻可搜，再后台增量重建，避免阻塞启动。
	if wd := strings.TrimSpace(a.convWorkDir()); wd != "" {
		codeidx.LoadCached(wd)
		if time.Since(a.idxAt) > 30*time.Second {
			a.idxAt = time.Now()
			codeidx.BuildAsync(wd)
		}
		// @ 文件补全的清单也在后台预热：用户第一次打 @ 时不再遍历磁盘。
		a.startWorkFilesScan(wd)
	}
}

// Attach 绑定窗口，同时挂上 PrintScreen 截图钩子。
// 钩子安装失败（被安全软件拦、或已有别的全局钩子抢占）不影响其它功能，
// 工具栏的相机按钮走的是同一条截图路径，可作兜底入口。
func (a *App) Attach(w *ui.Window) {
	a.w = w
	// 全局低级键盘钩子只用于 PrintScreen。它属于系统级资源，排障时可
	// 用 WIN7H_NOSNIP=1 关闭，确认某些"启动/显示卡住"是否与钩子有关。
	if strings.TrimSpace(os.Getenv("WIN7H_NOSNIP")) != "1" {
		w.EnableSnip(a.onSnipCapture)
	}
	a.startWatchdog()
}

// confirm 由引擎 goroutine 调用，阻塞直到用户响应。
func (a *App) confirm(title, detail string) bool {
	req := confirmReq{Title: title, Detail: detail, Resp: make(chan bool, 1)}
	select {
	case a.confirmCh <- req:
	case <-time.After(60 * time.Second):
		// 投递超时不能直接当成"用户拒绝"：UI 线程在存会话（gzip）、
		// 或同时有多路审批时，5 秒很容易撞上，过去会造成"没人点过，
		// 模型却以为被拒绝"，然后自作主张换方案。这里拉长到 60 秒并留日志。
		core.Logf("审批请求投递超时（%s）：%s", title, detail)
		return false
	}

	// v1.28 双通道：电脑弹窗与手机指挥台谁先答都算数。
	// 手机上没开指挥台时 remoteAns 是 nil，这里就会一直等电脑 —— 与旧行为完全一致。
	var remoteAns chan bool
	if a.v27 != nil && a.v27.rctl != nil && a.v27.rctl.ApprovalHub() {
		remoteAns = make(chan bool, 1)
		title, detail := title, detail
		core.SafeGo("app/app.go:365", func() {
			allow, ok := a.v27.rctl.AskApproval(title, detail, "confirm", 10*time.Minute)
			if !ok {
				close(remoteAns)
				return
			}
			remoteAns <- allow
		})
	}

	select {
	case v := <-req.Resp:
		return v
	case v, ok := <-remoteAns:
		if !ok {
			// 手机上超时没答：回到"只听电脑"的语义，不能因为没人看手机就一直挂住。
			select {
			case v2 := <-req.Resp:
				return v2
			case <-time.After(10 * time.Minute):
				return false
			}
		}
		// 手机已作答：把电脑端弹窗撤掉，避免用户看到一个已经无效的确认框。
		a.remoteClose.Store(true)
		a.Redraw()
		return v
	case <-time.After(10 * time.Minute):
		a.remoteClose.Store(true)
		a.Redraw()
		return false
	}
}

func (a *App) toast(text string, isErr bool) {
	now := time.Now()
	t := toast{Text: text, At: now, Until: now.Add(3200 * time.Millisecond), Err: isErr}
	a.tst = &t
	// 保留最近 20 条：提示被覆盖后仍能从通知历史里查到，不再"闪一下就没了"。
	a.tstHistory = append(a.tstHistory, t)
	if len(a.tstHistory) > 20 {
		a.tstHistory = a.tstHistory[len(a.tstHistory)-20:]
	}
}

// Redraw 请求重绘。
func (a *App) Redraw() {
	if a.w != nil {
		a.w.Invalidate()
	}
}

/* ---------------- ui.App 实现 ---------------- */

// Resize 记录尺寸。
func (a *App) Resize(w, h int32) {
	a.beat()
	a.winW, a.winH = w, h
	a.layout()
}

// easeOut 缓出曲线：侧栏展开先快后慢，收尾干净。
func easeOut(t float32) float32 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	u := 1 - t
	return 1 - u*u*u
}

// 面板尺寸常量：宽度是 96dpi 逻辑像素，绘制时由画布按 DPI 缩放。
const (
	defSideW int32 = 264 // 侧栏默认宽度（DESIGN 第 5 节）
	defInspW int32 = 320 // 检查器默认宽度（DESIGN 第 5 节）
	minSideW int32 = 180
	maxSideW int32 = 460
	minInspW int32 = 200
	maxInspW int32 = 560
	// railPanelW 是唯一的左侧栏宽度。v1.40.1 起取消「图标 rail + 会话侧栏」
	// 两层结构：新建会话、最近会话、全部会话、工作区、设置与工具全部在同一栏里，
	// 视觉与信息架构对齐 WorkBuddy（见 docs/UI重设计-v1.40.1.md）。
	railPanelW int32 = 264
	minChatW   int32 = 520 // 主区最小宽度（DESIGN 第 5 节）
	handleGrab int32 = 5   // 分隔条抓取半径
)

// layoutLogOn 读取布局探针开关：WIN7H_LAYOUT=1 时把每次布局变化写进日志。
// 抽屉/挤压这类问题靠肉眼看截图只能猜，把"窗口宽、各面板占位、是否浮层"
// 打出来，才能一眼看出是谁把主区挤没了。
var layoutLogOn = strings.TrimSpace(os.Getenv("WIN7H_LAYOUT")) == "1"

// overlayAlpha 是抽屉遮罩的不透明度。0.32 是"看得出被压住、但内容还读得清"的
// 折中：再深就把会话正文糊成灰块，再浅就分不出浮层。
const overlayAlpha = 0.22

// ScaleChanged 响应系统 DPI 变化：重排布局并刷新一帧。
// 逻辑像素尺寸由 wi.Canvas 换算得出，所以这里只需重新算一次布局。
func (a *App) ScaleChanged(scale float32) {
	if a.w != nil {
		if c := a.w.Canvas(); c != nil {
			a.winW, a.winH = c.W(), c.H()
		}
	}
	a.layout()
	a.ver++
}

/* ---------------- 主聊天界面 ---------------- */

// startRename 进入某个会话的就地改名状态。
func (a *App) startRename(i int) {
	if i < 0 || i >= len(a.convs) {
		return
	}
	a.editingID = i
	a.convRename = a.convs[i].Title
	a.ui.SetField("conv.ren", a.convs[i].Title)
	a.ui.SetFocus("conv.ren")
	a.Redraw()
}

// commitRename 提交改名。
func (a *App) commitRename(save bool) {
	i := a.editingID
	if i < 0 {
		return
	}
	name := a.convRename
	a.editReset()
	if save {
		a.renameConv(i, name)
		a.sessDirty = true
	}
	a.Redraw()
}

// editReset 退出改名状态，并把焦点还给输入区。
func (a *App) editReset() {
	if a.editingID < 0 {
		return
	}
	a.editingID = -1
	a.convRename = ""
	if a.page == PageChat {
		a.ui.SetFocus("input")
	}
}

func (a *App) inputHeight() int32 { return a.inputHeightBox() }

/* ---------------- 交互 ---------------- */

type inputLine struct{ start, end int }

// inputLineBounds 按换行把输入文本切成行，供跨行光标与鼠标定位复用。
func (a *App) inputLineBounds() []inputLine {
	out := []inputLine{{start: 0, end: 0}}
	for i, ch := range a.input {
		if ch == '\n' {
			out[len(out)-1].end = i
			out = append(out, inputLine{start: i + 1, end: i + 1})
		}
	}
	out[len(out)-1].end = len(a.input)
	return out
}

func (a *App) selRange() (int, int) {
	if a.inputSel < 0 {
		return -1, -1
	}
	lo, hi := a.inputSel, a.inputCur
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo < 0 {
		lo = 0
	}
	if hi > len(a.input) {
		hi = len(a.input)
	}
	return lo, hi
}

func (a *App) pushInputUndo() {
	cp := append([]rune(nil), a.input...)
	a.inputUndo = append(a.inputUndo, cp)
	if len(a.inputUndo) > 50 {
		a.inputUndo = a.inputUndo[len(a.inputUndo)-50:]
	}
}

// pageDefaultAction 让 Enter 在非聊天页触发该页的主按钮。
func (a *App) pageDefaultAction() {
	switch a.page {
	case PageOnboard:
		switch {
		case a.onbStep == 0:
			a.onbGo(1)
		case a.ui.Focus() == "ob.back":
			a.onbGo(0)
		default:
			a.finishOnboard()
		}
	case PageSettings:
		a.saveSettings()
	case PageDiag, PageCompare, PageAuto, PageExt, PageUsage, PageNotices:
		a.page = PageChat
	}
}

// maxInputRunes 是输入框硬上限。32 位进程里 100 万字符粘贴 + 50 层撤销
// 会占用数百 MB；20 万字符足够放大型报错/日志，又不会把地址空间打爆。
const maxInputRunes = 200_000

func (a *App) insertInput(s string) {
	a.pushInputUndo()
	if lo, hi := a.selRange(); lo >= 0 {
		a.input = append(a.input[:lo], a.input[hi:]...)
		a.inputCur = lo
		a.inputSel = -1
	}
	rs := []rune(s)
	room := maxInputRunes - len(a.input)
	if room < 0 {
		room = 0
	}
	truncated := false
	if len(rs) > room {
		rs = rs[:room]
		truncated = true
	}
	out := make([]rune, 0, len(a.input)+len(rs))
	out = append(out, a.input[:a.inputCur]...)
	out = append(out, rs...)
	out = append(out, a.input[a.inputCur:]...)
	a.input = out
	a.inputCur += len(rs)
	a.refreshComplete()
	if truncated {
		a.toast("输入内容过长，已截断到 20 万字符", true)
	}
}

// CaretPos 供 ui.CaretApp 使用：把输入法组字窗口定位到当前光标。
func (a *App) CaretPos() (int32, int32, bool) {
	if a.page == PageChat && a.ui.Focus() == "input" {
		return a.curCaretX, a.curCaretY + 20, true
	}
	if x, y, ok := a.ui.Caret(); ok {
		return x, y + 20, true
	}
	return 0, 0, false
}

// proxyHint 返回当前生效的代理来源，3 秒缓存一次，避免每帧读注册表。
func (a *App) proxyHint() string {
	mode := "system"
	if a.cfg != nil && a.cfg.ProxyMode != "" {
		mode = a.cfg.ProxyMode
	}
	if mode == "direct" {
		return "直连：不读系统代理，也不读取环境变量代理"
	}
	if mode == "manual" {
		if a.cfg != nil && strings.TrimSpace(a.cfg.ProxyURL) != "" {
			return "手动代理：" + a.cfg.ProxyURL
		}
		return "手动代理：尚未填写地址"
	}
	// 系统代理的注册表读取在后台 pageData 刷新器里做，绘制线程只取快照。
	if txt := a.uiData().sysProxy; txt != "" {
		return txt
	}
	return "系统代理：读取中…"
}

func firstProxyEnv() string {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return k + "=" + v
		}
	}
	return ""
}

func (a *App) Cursor() int {
	if a.modal != nil {
		return 0
	}
	if a.dragMode != 0 || a.handleAt(a.mx, a.my) != 0 {
		return 3 // 左右箭头：提示这里可以拖动
	}
	if a.page == PageChat {
		h := a.inputHeight()
		if a.my > a.winH-h && a.mx > a.railW+a.sideW {
			return 1
		}
	}
	// 引导页/首帧时 a.ui 还没初始化，WM_SETCURSOR 可能先到。
	if a.ui != nil {
		for _, h := range a.ui.hits {
			if h.rect.Contains(a.mx, a.my) {
				return 2
			}
		}
	}
	return 0
}

func (a *App) queueUI(fn func()) {
	if fn == nil {
		return
	}
	a.uiMu.Lock()
	a.uiQueue = append(a.uiQueue, fn)
	a.uiMu.Unlock()
}

// runUIQueue 在 Timer 里执行 Paint 期间排队的慢动作（保存、系统对话框等）。
// 执行前先清空切片：动作里再 queueUI 的会落到下一轮，避免同一帧无限递归。
func (a *App) runUIQueue() {
	a.uiMu.Lock()
	q := a.uiQueue
	a.uiQueue = nil
	a.uiMu.Unlock()
	if len(q) == 0 {
		return
	}
	for _, fn := range q {
		fn()
	}
}

func (a *App) scrollToBottom() {
	clipH := float32(a.winH) - 46 - float32(a.inputHeight()) - 12
	max := float32(a.contentH) - clipH
	if max < 0 {
		max = 0
	}
	a.scroll = max
}

// clampChatScroll 把会话滚动夹到本帧真实内容高度内。
// auto=true 时强制贴底；内容高度变小（流式收尾）时也不会停在旧底部的空白区。
func clampChatScroll(scroll, contentH, viewH float32, auto bool) float32 {
	max := contentH - viewH
	if max < 0 {
		max = 0
	}
	if scroll < 0 {
		scroll = 0
	}
	if auto || scroll > max {
		scroll = max
	}
	return scroll
}

// streamMsg 返回当前正在追加的助手消息。
func (a *App) streamMsg() *Message {
	if a.curAsst >= 0 && a.curAsst < len(a.msgs) {
		return &a.msgs[a.curAsst]
	}
	return nil
}

func (a *App) newAssistantMsg() *Message {
	a.msgs = append(a.msgs, Message{Role: "assistant", At: time.Now(), Streaming: true})
	a.curAsst = len(a.msgs) - 1
	return &a.msgs[a.curAsst]
}

// stopGeneration 统一收尾"停止生成"：停引擎、清界面状态、收日志、落盘。
// 过去只调 Stop()，引擎不发终态，界面会一直停在"生成中"，发送按钮再也点不动。
// setPermMode 切换审批模式并立即落盘。
// 界面「权限」分区与 /perm 命令共用这一条路径，避免两处状态不一致。
func (a *App) setPermMode(m string) {
	if a.eng == nil || a.eng.Policy == nil {
		a.toast("审批策略尚未初始化", true)
		return
	}
	if a.streaming || a.eng.Running() {
		a.toast("生成中不能切换权限模式，先停止或等本轮结束", true)
		return
	}
	a.eng.Policy.Mode = m
	a.savePolicy()
	label := map[string]string{"auto": "自动执行", "ask": "每次确认", "plan": "只读计划"}[m]
	if label == "" {
		label = m
	}
	a.toast("审批模式："+label, false)
}

// cancelPendingConfirms 撤销所有等待中的审批请求。
//
// 每个请求都必须回一个 false：只清队列不回结果，引擎会一直阻塞到 10 分钟超时，
// 这段时间 SendWithImages 见到 running 会静默丢弃用户的新消息 —— 这就是
// "点了停止之后再也发不出去"的根因。
func (a *App) cancelPendingConfirms() {
	for _, req := range a.pendingConfirms {
		select {
		case req.Resp <- false:
		default:
		}
	}
	a.pendingConfirms = nil
	for _, req := range a.pendingDiffs {
		select {
		case req.Resp <- nil:
		default:
		}
	}
	a.pendingDiffs = nil
	for _, req := range a.pendingPlans {
		select {
		case req.Resp <- planDecision{Action: "reject"}:
		default:
		}
	}
	a.pendingPlans = nil
	a.modalOKFocused = false
	if a.modal != nil {
		switch a.modal.Kind {
		case modalDiff:
			a.modalCancelDiff()
		case modalPlan:
			a.modalAnswerPlan("reject", "")
		case modalPassword:
			a.cancelPasswordModal()
		default:
			if a.modal.Confirm {
				a.modalAnswer(false)
			}
		}
	}
}

func (a *App) stopGeneration() {
	if a.eng != nil {
		a.eng.Stop()
	}
	// 停止之后用户再点"允许"不该再生效。
	a.cancelPendingConfirms()
	a.streaming = false
	a.curAsst = -1
	a.statusMsg = "已停止"
	if m := a.streamMsg(); m != nil {
		m.Streaming = false
	}
	a.journalFinish()
	a.saveConv()
}

func (a *App) send() {
	a.comp = nil
	a.compIdx = 0
	text := strings.TrimSpace(string(a.input))
	imgs := append([]string(nil), a.pendingImgs...)
	// 斜杠命令是本地能力，必须在交给模型之前拦下。
	// " // " 开头（双斜杠）是用户真想发一段以 / 开头的文本，转义后照常发送。
	if strings.HasPrefix(text, "//") {
		text = text[1:]
	} else if strings.HasPrefix(text, "/") && a.handleSlash(text) {
		return
	}
	if (text == "" && len(imgs) == 0) || a.streaming {
		return
	}
	if a.eng == nil {
		// 配置存在但模型名为空（旧配置/手改配置）时也要给一条可执行的出路，
		// 而不是点了发送毫无反应。
		a.toast("还没有可用模型：在左下角设置里填 Base URL、Key 和模型 ID", true)
		a.page = PageSettings
		a.settingsScrl = 0
		return
	}
	if text == "" {
		// 只发图片时给一个占位文本，避免部分服务端拒绝空 content
		text = "请看这张图。"
	}
	// 记入跨会话提示词历史（Ctrl+R 用）。只在真正发给模型时记，
	// 斜杠命令与空输入不算；写盘失败不阻断发送。
	if (text != "请看这张图。" || len(imgs) > 0) && len(text) <= 4000 {
		a.promptHistory = core.AddPromptHistory(a.promptHistory, text)
		_ = core.SavePromptHistory(a.promptHistory)
	}
	// 用户点了"按上面的计划开始执行"：此时必须退出计划模式，
	// 否则写工具仍被拦，用户会以为程序坏了。
	if a.eng.PlanMode && strings.HasPrefix(text, "按上面的计划开始执行") {
		a.eng.PlanMode = false
		a.statusMsg = ""
	}
	a.msgs = append(a.msgs, Message{Role: "user", Text: text, Images: imgs, At: time.Now()})
	a.pendingImgs = nil
	a.input = nil
	a.inputCur = 0
	a.curAsst = -1
	a.streaming = true
	a.autoScroll = true
	a.ver++
	conv := a.currentConv()
	if conv != nil && (conv.Title == "" || conv.Title == "新对话") {
		t := []rune(text)
		if len(t) > 18 {
			t = append(t[:18], '…')
		}
		conv.Title = string(t)
	}
	// 睡眠/网络切换期间代理可能变了；当时若有请求在跑，先记脏，
	// 到新一轮发送前（此时没有并发请求）再替换 transport.Proxy。
	if a.proxyDirty {
		a.applyProxy()
		a.proxyDirty = false
	}
	// 先落一条"轮次开始"，掉电后据此提示续做。
	a.journalBegin(text)
	a.eng.SendWithImages(a.withAtRefs(text), imgs)
	a.saveConv()
	a.scrollToBottom()
}

// withAtRefs 在发送前展开 @ 文件引用（仅模型可见，界面保留原文）。
func (a *App) withAtRefs(text string) string {
	if a.eng == nil {
		return text
	}
	return core.ExpandAtRefs(text, a.eng.WorkDir)
}

func (a *App) newConv() {
	if a.streaming {
		// 生成中新建会话会把旧引擎的流式事件落到新会话上，也是“点一下突然
		// 退出/串台”的来源之一。先让用户明确停止，再新建。
		a.toast("生成中不能新建会话：先点停止，或等这一轮结束", true)
		return
	}
	a.sessDirty = true
	a.saveConv()
	a.convs = append([]*Conv{{ID: newConvID(), Title: "新对话", At: time.Now(), Msgs: []Message{}, Loaded: true}}, a.convs...)
	a.active = 0
	a.msgs = nil
	a.curAsst = -1
	a.scroll = 0
	a.pendingImgs = nil
	a.comp = nil
	a.compIdx = 0
	a.ver++
	// 新会话按全局默认重建引擎：上一个会话钉住的模型/工作区不会被继承
	a.rebuildEngine()
	a.page = PageChat
}

func (a *App) currentConv() *Conv {
	if a.active >= 0 && a.active < len(a.convs) {
		return a.convs[a.active]
	}
	return nil
}

func (a *App) loadConv() {
	if a.streaming {
		a.toast("生成中不能切换会话：先点停止，或等这一轮结束", true)
		return
	}
	a.editReset()
	a.convScrl = 0
	// 换会话会整块换掉消息流；旧页面的命令前缀可能大面积相同，
	// 只靠脏区会在底部/侧栏留下上一会话的残影。强制一帧全量。
	a.forceFull = true
	if cv := a.currentConv(); cv != nil && !cv.Loaded {
		// 启动时只装了索引空壳：正文在后台读，读完再走一遍 loadConv。
		a.loadConvAsync(cv.ID, func(*Conv) { a.loadConv() })
		return
	}
	// 切会话即切环境：模型与工作区按目标会话重新解析，避免串用别的会话设置
	if conv := a.currentConv(); conv != nil && conv.WorkDir != "" {
		if err := core.ValidateWorkDir(conv.WorkDir); err != nil {
			core.SetStartupWarning("会话工作区不可用（" + err.Error() + "），已改用默认工作区")
			conv.WorkDir = ""
		}
	}
	a.rebuildEngine()
	if conv := a.currentConv(); conv != nil {
		core.SetCurrentConv(conv.ID)
		conv.Loaded = true
		conv.MsgN = len(conv.Msgs)
		conv.Preview = convLastPreview(conv)
		a.msgs = conv.Msgs
		a.pendingImgs = nil
		appImgs.Clear()
		if a.eng != nil {
			a.eng.Reset()
			if len(conv.EngineMsgs) > 0 {
				// 新格式：恢复结构化轨迹（含工具调用/结果），重启后模型仍知道改过什么。
				a.eng.LoadHistory(conv.EngineMsgs)
			} else {
				// 旧会话兼容：只恢复 user/assistant 文本，工具细节不重放。
				for _, m := range conv.Msgs {
					if m.Role == "user" || (m.Role == "assistant" && m.Text != "") {
						text := m.Text
						// 历史图片不重复上送（每张都带会迅速吃满上下文），只在文本里标注
						if len(m.Images) > 0 {
							text += fmt.Sprintf("\n（此处原有 %d 张图片，历史上下文已省略）", len(m.Images))
						}
						a.eng.Messages = append(a.eng.Messages, llm.Message{Role: m.Role, Content: text})
					}
				}
			}
		}
		a.scroll = conv.Scroll
		a.ver++
	}
}

// RefreshTheme 在 Windows 高对比/主题切换后重新解析配色。
// 系统高对比优先于用户偏好；关闭高对比后自动回到 dark/light。
func (a *App) RefreshTheme() {
	if a.th == nil {
		return
	}
	th := ui.ResolveTheme(a.cfg.Theme)
	if a.th.Name == th.Name {
		return
	}
	a.th = th
	ui.SetTokens(th.Tokens())
	a.forceFull = true
	a.ver++
}

func (a *App) toggleTheme() {
	if a.th.Name == "light" {
		a.cfg.Theme = "dark"
	} else {
		a.cfg.Theme = "light"
	}
	a.th = ui.ResolveTheme(a.cfg.Theme)
	_ = a.cfg.Save()
	// 整套配色换了：必须整帧重绘，否则未变化的命令会残留旧主题的像素。
	a.forceFull = true
	a.Redraw()
}

/* ---------------- 弹窗 ---------------- */

type confirmReq struct {
	Title  string
	Detail string
	Resp   chan bool
}

// ModalKind 区分三种模态：普通确认、富差分、方案审批。
type ModalKind int

const (
	modalConfirm ModalKind = iota
	modalDiff
	modalPlan
	modalPassword
)

// Modal 模态确认框。
type Modal struct {
	Kind   ModalKind
	Title  string
	Detail string
	Resp   chan bool
	// Confirm 保留旧字段，true 表示这是一个确认型弹窗。
	Confirm   bool
	Scroll    float32
	ScrollMax float32

	// 富差分（modalDiff）
	Path         string
	Hunks        []core.DiffHunk
	AllowPartial bool
	HunkAccept   map[int]bool
	DiffResp     chan []int

	// 方案审批（modalPlan）
	Plan     string
	Steps    int
	PlanResp chan planDecision

	// OnOK 非空时，确认后在 UI 线程上执行（删除会话这类本地操作不走引擎审批通道，没有 Resp 可等）。
	OnOK func()

	// 便携模式 U 盘密码弹窗（modalPassword）
	PasswordConfirm bool
	PasswordHint    string
	PasswordErr     string
	PasswordForget  bool // 解锁模式显示“忘记密码”
	PasswordOK      func(string) error
	PasswordCancel  func()
}

func (a *App) modalRect() ui.Rect {
	w := int32(560)
	h := int32(300)
	if a.modal != nil && a.modal.Kind != modalConfirm {
		w = 760
		h = 520 // 差分内容可能很长，留足空间并用滚轮
		if a.modal.Kind == modalPlan {
			h = 380 // 方案一般几行就够，不需要一大片空白
		}
		if a.modal.Kind == modalPassword {
			w = 520
			h = 320
			if a.modal.PasswordConfirm {
				h = 380
			}
		}
	}
	if a.winW < 700 {
		w = a.winW - 80
	}
	if a.winH < 420 {
		h = a.winH - 80
	}
	return ui.R((a.winW-w)/2, (a.winH-h)/2, w, h)
}

func (a *App) paintModal(c *ui.Canvas) {
	th := a.th
	u := a.ui
	// 遮罩
	c.FillRect(ui.R(0, 0, a.winW, a.winH), 0x0A0C10)
	r := a.modalRect()
	c.Round(r, 8, th.Surface)
	c.RoundStroke(r, 8, th.Border)

	c.TextFace(r.X+24, r.Y+20, a.modal.Title, ui.FsLg, 700, th.Text, ui.FaceUI)
	c.HLine(r.X+24, r.Y+52, r.W-48, th.BorderSoft)

	// 富差分与方案审批各有专用渲染；普通确认沿用原来的等宽纯文本详情。
	switch a.modal.Kind {
	case modalDiff:
		a.paintDiffModal(c, r, u, th)
		return
	case modalPlan:
		a.paintPlanModal(c, r, u, th)
		return
	case modalPassword:
		a.paintPasswordModal(c, r, u, th)
		return
	}

	// 详情：等宽字体，保留换行
	body := ui.R(r.X+24, r.Y+64, r.W-48, r.H-140)
	c.Clip(body)
	ty := body.Y
	for _, ln := range strings.Split(a.modal.Detail, "\n") {
		if ty > body.Bottom() {
			break
		}
		c.TextFace(body.X, ty, ln, ui.FsSm, 400, th.TextDim, ui.FaceMono)
		ty += 20
	}
	c.PopClip()

	// 按钮
	by := r.Bottom() - 60
	br := ui.R(r.Right()-220, by, 94, 34)
	if u.Button("modal.cancel", "取消", br, BtnGhost) {
		a.modalAnswer(false)
	}
	br2 := ui.R(r.Right()-118, by, 94, 34)
	if u.Button("modal.ok", "允许执行", br2, BtnDanger) {
		a.modalAnswer(true)
	}
	c.TextFace(r.X+24, by+9, "回车默认取消 · Tab 切换到「允许执行」后才可确认", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	focusR := br
	if a.modalOKFocused {
		focusR = br2
	}
	c.RoundStroke(focusR.Inset(-3), 8, th.InputFocus)
}

func (a *App) modalClick(x, y int32) {
	id := a.ui.HandleClick(x, y)
	// 兜底：模态的命中区必须最后注册，但在某些绘制时序（图层重放、
	// 上一帧页面按钮残留）下 HitAt 可能返回下层控件。直接用按钮几何再判一次，
	// 保证「允许执行 / 取消」鼠标点击一定有效，不用先 Tab 切焦点。
	if id == "" {
		r := a.modalRect()
		by := r.Bottom() - 60
		if ui.R(r.Right()-118, by, 94, 34).Contains(x, y) {
			id = "modal.ok"
		} else if ui.R(r.Right()-220, by, 94, 34).Contains(x, y) {
			id = "modal.cancel"
		}
	}
	if a.modal != nil && a.modal.Kind == modalPassword {
		switch id {
		case "modal.pwd.ok":
			a.submitPasswordModal()
		case "modal.pwd.cancel":
			a.cancelPasswordModal()
		case "modal.pwd.show":
			a.togglePasswordVisible()
		case "modal.pwd.forget":
			a.forgetPortablePassword()
		}
		return
	}
	if a.modal != nil && a.modal.Kind == modalDiff {
		switch {
		case id == "modal.ok":
			a.modalAnswerDiff(false)
		case id == "modal.cancel":
			a.modalCancelDiff()
		case strings.HasPrefix(id, "modal.hunk:"):
			// 按钮自身的 u.Button 已记录点击；这里只负责触发一次重绘，
			// 真正的状态翻转在 paintDiffModal 的 Button 分支里完成。
			a.Redraw()
		}
		return
	}
	if a.modal != nil && a.modal.Kind == modalPlan {
		switch id {
		case "plan.approve":
			a.modalAnswerPlan("approve", "")
		case "plan.reject":
			a.modalAnswerPlan("reject", "")
		case "plan.modify":
			a.modalAnswerPlan("modify", a.modal.Plan)
		}
		return
	}
	switch id {
	case "modal.ok":
		a.modalAnswer(true)
	case "modal.cancel":
		a.modalAnswer(false)
	}
}

func (a *App) modalAnswer(ok bool) {
	if a.modal == nil {
		return
	}
	if a.modal.Resp != nil {
		select {
		case a.modal.Resp <- ok:
		default:
		}
	}
	onOK := a.modal.OnOK
	a.modal = nil
	a.modalOKFocused = false
	if ok && onOK != nil {
		onOK()
	}
	// 立刻把队列里的下一个弹窗挂上，不让引擎空等超时。
	a.installNextModal()
}

func (a *App) paintToast(c *ui.Canvas) {
	if a.tst == nil {
		return
	}
	th := a.th
	msg := a.tst.Text
	rw := c.MeasureFace(msg, ui.FsSm, 400, ui.FaceUI) + 32
	if rw > a.winW-100 {
		rw = a.winW - 100
		msg = ellipsis(c, msg, ui.FsSm, 400, rw-32)
	}
	ty := a.winH - 140
	if a.page == PageOnboard {
		// 引导页的底部就是「上一步 / 开始使用」，提示改到顶部，免得压住按钮。
		ty = 24
	}
	r := ui.R((a.winW-rw)/2, ty, rw, 34)
	bg := th.Card
	if a.tst.Err {
		bg = ui.Blend(th.Card, th.Danger, 0.35)
	}
	c.Round(r, 8, bg)
	c.RoundStroke(r, 8, th.Border)
	c.TextFace(r.X+16, r.Y+9, msg, ui.FsSm, 400, th.Text, ui.FaceUI)
}

var _ = os.Getenv
var _ = filepath.Join

// usageSummary 生成顶栏常驻的用量摘要。
func (a *App) usageSummary() string {
	bs := a.eng.Budget()
	if bs.Turns == 0 && bs.TokIn == 0 && bs.TokOut == 0 {
		return ""
	}
	line := fmt.Sprintf("%s token · $%.4f", compactNum(bs.TokIn+bs.TokOut), bs.CostUSD)
	if bs.Exceeded {
		line += " · 已超预算"
	} else if bs.Warn {
		line += " · 接近上限"
	}
	return line
}

// compactNum 把 token 数压缩成 1.2k / 3.4M 的短形式。
func compactNum(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

// noteStartupWarning 把"上次异常退出"写进对话，但同一条绝不重复堆。
//
// 反复启动（强制结束、调试、断电恢复）时每次都会生成一条提示，
// 十几条一模一样的"没有正常退出"会把整个会话淹掉——用户翻历史时
// 看到的就是满屏重复文字，只会觉得这东西是半成品。
// 这里先把末尾已经存在的同类提示全部摘掉，再追加一条最新的。
func (a *App) noteStartupWarning(w string) {
	const mark = "没有正常退出"
	n := len(a.msgs)
	for n > 0 && a.msgs[n-1].Role == "assistant" && strings.Contains(a.msgs[n-1].Text, mark) {
		n--
	}
	if n != len(a.msgs) {
		a.msgs = a.msgs[:n]
	}
	a.sysNote(w)
}
