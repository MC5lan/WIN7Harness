//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"win7harness/internal/agent"
	"win7harness/internal/bootstrap"
	"win7harness/internal/codeidx"
	"win7harness/internal/core"
	"win7harness/internal/llm"
	"win7harness/internal/mcp"
	"win7harness/internal/skills"
	"win7harness/internal/ui"
	"win7harness/internal/ui/kit"
	"win7harness/internal/win32"
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

// Page 当前页面。
type Page int

const (
	PageChat Page = iota
	PageOnboard
	PageSettings
	PageDiag
	PageCompare
	PageAuto
	PageExt
	PageUsage
	PageSessions
	PageWorkspace
	PageCosts
	PageNotices
)

// Conv 一个会话。字段带 json tag，直接作为落盘格式（见 store.go）。
type Conv struct {
	ID    string    `json:"id"`
	Title string    `json:"title"`
	Msgs  []Message `json:"msgs"`
	At    time.Time `json:"at"`
	Tok   int       `json:"tok"`
	// Pinned 置顶会话：长期在用的项目对话置顶后不会随新会话沉下去。
	Pinned bool `json:"pinned,omitempty"`

	// Model / Prov 是会话级模型覆盖。空表示跟随全局默认模型；
	// Prov 记录模型所在端点，换模型不会串用别的服务商地址与密钥。
	Model string `json:"model,omitempty"`
	Prov  string `json:"prov,omitempty"`
	// WorkDir 是会话级工作区。空表示跟随全局默认工作目录。
	WorkDir string `json:"work_dir,omitempty"`
	// WorktreeFrom / WorktreeBranch 记录本会话是否在 git worktree 隔离副本里：
	// WorktreeFrom 是原仓库根，WorktreeBranch 是隔离分支。空表示普通目录。
	WorktreeFrom   string `json:"worktree_from,omitempty"`
	WorktreeBranch string `json:"worktree_branch,omitempty"`
	// Scroll 记住每条会话自己的滚动位置，切回来不跳回顶部。
	Scroll float32 `json:"scroll,omitempty"`

	// —— 以下为运行期字段，不落盘 ——
	// Loaded 表示 Msgs 是否已经完整读入。启动时只读会话索引，最近若干条
	// 对话在后台补全，其余等用户点开再读，避免大历史库拖慢开窗。
	Loaded bool `json:"-"`
	// MsgN 是磁盘上的消息条数（未 Loaded 时用于侧栏计数）。
	MsgN int `json:"-"`
	// Preview 是索引生成的最后一条文本摘要（未 Loaded 时用于搜索标题/预览）。
	Preview string `json:"-"`
}

// toast 轻提示。
type toast struct {
	Text  string
	At    time.Time
	Until time.Time
	Err   bool
}

// App 实现 ui.App，是全部界面与业务逻辑的粘合层。
type App struct {
	// foldOpen 记录被手动展开的折叠工具组，键见 ToolGroupKey。
	foldOpen map[string]bool
	// evMu 串行化引擎事件回调：子 Agent 并行时多个 goroutine 会同时 emit，
	// 而 onEvent 会直接改消息与界面状态，必须互斥。
	evMu sync.Mutex

	w  *ui.Window
	th *ui.Theme
	ui *UI

	cfg *core.Config
	eng *agent.Engine
	// runMode 是启动时算好的安装形态（标准 / 所有用户 / 便携），
	// 设置页每帧直接读它，避免每帧探测 U 盘写权限。
	runMode string

	// v27 存放 v1.27 后台能力（手机指挥台/定时任务/目录守护/更新检查）的状态，
	// 集中一处避免结构体继续膨胀。
	v27 *v127State
	// v29 存放 v1.29 任务队列 / 运行指标 / 设备表。
	v29 *v129State
	// perf 是 UI 心跳、卡顿转储与帧成本探针（见 perf.go）。
	perf *perfState

	page     Page
	convs    []*Conv
	active   int
	convScrl float32
	diag     diagState

	settingsScrl float32
	settingsH    int32
	// modelsCache 保存从端点 /models 动态拉到的模型 ID，键是规范化 BaseURL。
	// 只存在内存里：它属于端点元数据，不需要写进配置文件。
	modelsCache map[string][]string

	palOpen bool
	palQ    []rune
	palCur  int
	palIdx  int
	palRect ui.Rect

	// 跨会话提示词历史与 Ctrl+R 搜索浮层
	promptHistory []string
	histOpen      bool
	histQ         []rune
	histCur       int
	histIdx       int
	histResults   []string
	histRect      ui.Rect

	// menu 是通用的下拉浮层（会话内切模型、切换工作区共用）。
	menu     *popupMenu
	menuIdx  int
	menuRect ui.Rect

	// —— v1.28 动态 ——
	trayInit bool
	trayOn   bool
	lastDiag time.Time
	qr       *core.QRCode
	qrURL    string
	qrUntil  time.Time

	msgs []Message

	// msgLinks 是本帧消息正文里检测到的可点击路径，下标即 "path:<i>" 命中 id。
	// 每帧排版时重建，与 ui.hits 的生命周期一致。
	msgLinks []msgLink
	// codeBlocks 是本帧可见代码块的可复制原文，key "<消息下标>:<块下标>"。
	codeBlocks []codeBlockEntry
	// pathCache 缓存消息内路径的 os.Stat 结果：杀软环境下一次 Stat 可能几十毫秒，
	// 而同一路径会在悬停/滚动时被反复校验。3 秒过期，点击打开时仍走实时校验。
	pathCache map[string]pathCacheEntry
	// changedCache 缓存工作区页的"本会话改动"列表，按内容版本重建。
	changedCache []changedFile
	changedVer   int
	changedWD    string

	// 排版缓存
	ver      int
	laySegs  []laySeg
	layKey   layKey
	contentH int32

	// 布局
	railW, sideW, inspW int32
	chatW, chatH        int32
	layW                int32
	sideA, inspA        float32
	animInit            bool
	winW, winH          int32
	showInsp            bool
	showSide            bool

	// 抽屉式面板：宽度可拖拽、窗口变窄时自动转为浮层抽屉。
	// sideWUser / inspWUser 是用户设定的目标宽度（记忆在配置里），
	// 实际绘制宽度 sideW / inspW 每帧按动画进度与窗口尺寸算出来。
	sideWUser, inspWUser int32
	// sideOcc / inspOcc 是本帧占位宽度：内嵌模式等于可视宽度，抽屉模式为 0，
	// 抽屉悬浮在会话区之上，不挤压对话内容。
	sideOcc, inspOcc int32
	sideDrawer       bool // 本帧侧栏是否以浮层抽屉呈现（不占位）
	inspDrawer       bool // 本帧检查器是否以浮层抽屉呈现（不占位）
	// dbgLayout 上一次写进日志的布局指纹（WIN7H_LAYOUT=1 时才用）。
	dbgLayout string
	// uiKey 是"当前页面/页签"的指纹。切换页面或页签时强制整帧重绘：
	// canvas 后端只重放与上一帧不同的命令；背景 Clear 命令每帧相同，
	// 新页面没覆盖到的区域会把旧页面的按钮/卡片残影留在屏幕上。
	uiKey string
	// autoTab 自动化页当前的选项卡（队列/定时/守护）。
	autoTab int
	// extTab 扩展页当前的选项卡（设备/手机/子代理/技能/MCP）。
	extTab int
	// useTab 用量页当前的选项卡（概览/成本/工具耗时）。
	useTab int
	// pdata 是需要读磁盘/系统状态的页面数据快照（见 pagedata.go）。
	// 后台刷新，绘制线程只读，页面不再因悬停/滚轮触发磁盘 I/O。
	pdata *pageDataState
	// kitui 是 kit 组件框架的全局 Ctx（Option A）。多个 kit 页面共享同一份 Ctx，
	// 维护滚动偏移 / 输入文本等跨帧状态；每个页面自己 BeginFrame() 并组装 root 即可。
	kitui     *kit.Ctx
	sessDirty bool
	sessIdx   *sessIndexState
	// ws 是工作区总览的后台扫描状态（磁盘遍历 / git 子进程）。
	ws *wsState
	// forceFull 要求下一帧整屏重绘（抽屉开合等拓扑变化时置位）。
	forceFull bool
	// dragMode 0 无 / 1 拖侧栏 / 2 拖检查器；dragX0+dragW0 记录按下时的基准。
	dragMode int
	dragX0   int32
	dragW0   int32
	// hbHover 鼠标悬停在分隔条上（用于高亮与光标），hbAt 双击复位检测。
	hbHover int
	hbAt    time.Time

	scroll     float32
	autoScroll bool
	// jumpMsg 待滚动到的消息下标（-1 表示无）。/open 命中结果时设置，
	// 下一帧排版完成后消费——此时才知道每条消息实际落在哪个 y。
	jumpMsg int
	// findHits 最近一次 /find 的结果，供 /open 按序号跳转。
	findHits []findHit
	// hlMsg / hlAt 跳转后的高亮目标与起始时间，2.6 秒内绘制强调边。
	hlMsg     int
	hlAt      time.Time
	streaming bool
	showThink bool
	curAsst   int // 正在流式输出的助手消息索引，-1 表示无

	input    []rune
	inputCur int
	// pendingImgs 待发送的图片（附件目录内的绝对路径）。
	pendingImgs []string
	// chipRects 输入区附件缩略图的命中区域，每帧刷新。
	chipRects            []ui.Rect
	curCaretX, curCaretY int32

	evCh       chan agent.Event
	confirmCh  chan confirmReq
	diffCh     chan diffReq
	planCh     chan planReq
	testCh     chan testResult
	modelsCh   chan modelListResult
	convLoadCh chan convLoadResult
	// doctorCh 是端点诊断结果：在 UI 线程消费，避免后台 goroutine 直接改消息。
	doctorCh   chan testResult
	doctorBusy bool
	// hydrateCh 是后台补全会话正文的结果：只替换列表里的空壳，不切换页面。
	hydrateCh chan *Conv
	modal     *Modal
	// uiQueue 把按钮回调里的慢动作排到 Timer 执行，Paint 只负责画和登记点击。
	uiQueue []func()
	// pwdPrompted 防止取消 U 盘密码框后每帧又弹出来。
	pwdPrompted bool
	// convLoading 是正在后台打开的会话 ID，防止连点重复读盘。
	convLoading string
	// lastHydrateAll 限制 /find 触发的全量正文补全频率。
	lastHydrateAll time.Time
	// saver 是会话异步落盘队列：JSON+gzip+备份不占 UI 线程。
	saver *convSaveQueue

	// remoteClose 由手机审批 goroutine 置位，主循环看到后撤掉电脑端弹窗。
	remoteClose atomic.Bool
	// shutdownDone 防止 WM_ENDSESSION 重复收尾导致会话队列被 stop 两次。
	shutdownDone atomic.Bool
	// proxyDirty 表示系统代理在睡眠/网络切换后变了，但当前有请求在跑；
	// 等下一轮发送前再替换，避免和 http.Transport 的并发读发生数据竞争。
	proxyDirty bool

	phase     float32
	mx, my    int32
	lastTick  time.Time
	statusMsg string
	// @ 文件补全状态
	comp          []compItem
	compIdx       int
	compAt        int
	compCache     []compItem
	compCacheAt   time.Time
	compCacheRoot string
	compCacheMu   sync.Mutex
	compScanBusy  bool
	compScanDone  chan struct{}
	idxAt         time.Time
	tst           *toast

	// 崩溃 / 断电恢复：轮次日志与上次未收尾的记录。
	jr          *core.Journal
	pendingRec  []core.JournalEntry
	recoverConv string
	// lastDisk 上次把消息写盘的时间，用于工具级增量保存的节流。
	lastDisk time.Time

	// 交互状态：按下/抬起配对、浮层吞点击、危险确认默认焦点、待处理确认队列。
	suppressClick   bool
	modalOKFocused  bool
	pendingConfirms []confirmReq
	pendingDiffs    []diffReq
	pendingPlans    []planReq
	inputSel        int // 选区另一端，-1 表示无选区
	inputUndo       [][]rune
	lastIdlePaint   time.Time
	tstHistory      []toast
	retryBackup     *Message
	inputRect       ui.Rect
	inputTx         int32
	inputTy         int32
	inputLineH      int32
	inputScroll     int32 // 输入框纵向滚动（视觉行）
	inputPinScroll  bool  // 滚轮手动滚动后暂不强制游标可见（下次编辑复位）

	// 表单（引导页 / 设置页）
	presetIdx      int
	testMsg        string
	testOK         *bool
	testing        bool
	fetchingModels bool
	modelsMsg      string
	modelsOK       *bool
	modelMenuPage  int
	modelMenuBase  string
	editingID      int    // 正在重命名的会话（a.convs 下标），-1 表示无
	convRename     string // 改名输入框的当前内容
	convListH      int32  // 会话列表内容高度，用于滚轮限位
	// settingsTab 是设置中心当前分组（模型/外观/权限/网络/预算/高级）。
	settingsTab int
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
	// 首次启动投放一个示例技能，用户 /skills 就能看到模板；已有技能时不打扰。
	skills.EnsureSample(cfg.WorkDir)
	a.uiDataDrop()
	// 会话中心索引在后台预热：用户点开"会话中心"时不用等第一次解析。
	core.SafeGo("app/pages_kit.go:preloadSessions", func() { a.triggerSessionIndex() })
	// MCP 服务在后台连接，失败不影响主界面
	go a.bootMCP(false)
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

// dense 低高度窗口（1280x720 一档）启用紧凑纵向间距。
func (a *App) dense() bool { return a.winH > 0 && a.winH < 800 }

// topH 返回顶栏高度。
func (a *App) topH() int32 {
	if a.dense() {
		return 40
	}
	return 46
}

// 面板尺寸常量：宽度是 96dpi 逻辑像素，绘制时由画布按 DPI 缩放。
const (
	defSideW   int32 = 264 // 侧栏默认宽度（DESIGN 第 5 节）
	defInspW   int32 = 320 // 检查器默认宽度（DESIGN 第 5 节）
	minSideW   int32 = 180
	maxSideW   int32 = 460
	minInspW   int32 = 200
	maxInspW   int32 = 560
	railPanelW int32 = 56
	minChatW   int32 = 520 // 主区最小宽度（DESIGN 第 5 节）
	handleGrab int32 = 5   // 分隔条抓取半径
)

func (a *App) layout() {
	ts, ti := float32(0), float32(0)
	if a.showSide {
		ts = 1
	}
	if a.showInsp {
		ti = 1
	}
	if !a.animInit { // 首帧直接就位，避免启动时侧栏滑入造成抖动
		a.animInit = true
		a.sideA, a.inspA = ts, ti
	}
	// 纯计算下沉到 layout_geom.go，便于用坐标断言验证"抽屉不挤压主区"。
	g := computePanelGeom(panelInput{
		WinW: a.winW, WinH: a.winH,
		ShowSide: a.showSide, ShowInsp: a.showInsp,
		SideUser: a.sideWUser, InspUser: a.inspWUser,
		SideA: a.sideA, InspA: a.inspA,
	})
	a.railW = g.RailW
	a.sideW = g.SideW
	a.inspW = g.InspW
	a.sideOcc = g.SideOcc
	a.inspOcc = g.InspOcc
	a.chatW = g.ChatW
	a.layW = g.LayW
	// 抽屉开合会整块挪动面板：拓扑变了就下一帧整屏重绘，别指望局部脏区能擦干净。
	if a.sideDrawer != g.SideDrawer || a.inspDrawer != g.InspDrawer {
		a.forceFull = true
	}
	a.sideDrawer = g.SideDrawer
	a.inspDrawer = g.InspDrawer
	a.chatH = a.winH
	a.logLayout(g)
}

// layoutLogOn 读取布局探针开关：WIN7H_LAYOUT=1 时把每次布局变化写进日志。
// 抽屉/挤压这类问题靠肉眼看截图只能猜，把"窗口宽、各面板占位、是否浮层"
// 打出来，才能一眼看出是谁把主区挤没了。
var layoutLogOn = strings.TrimSpace(os.Getenv("WIN7H_LAYOUT")) == "1"

// logLayout 只在布局结果发生变化时写一行，避免每帧刷屏。
func (a *App) logLayout(g panelGeom) {
	if !layoutLogOn {
		return
	}
	key := fmt.Sprintf("%d/%d|%d|%d|%d|%d|%d|%v%v", a.winW, a.winH, g.SideW, g.SideOcc,
		g.InspW, g.InspOcc, g.ChatW, g.SideDrawer, g.InspDrawer)
	if key == a.dbgLayout {
		return
	}
	a.dbgLayout = key
	core.Logf("布局: 窗口 %dx%d rail=%d 侧栏 %d(占位 %d, 抽屉 %v) 检查器 %d(占位 %d, 抽屉 %v) 主区 %d 排版宽 %d",
		a.winW, a.winH, g.RailW, g.SideW, g.SideOcc, g.SideDrawer,
		g.InspW, g.InspOcc, g.InspDrawer, g.ChatW, g.LayW)
}

// overlayAlpha 是抽屉遮罩的不透明度。0.32 是"看得出被压住、但内容还读得清"的
// 折中：再深就把会话正文糊成灰块，再浅就分不出浮层。
const overlayAlpha = 0.22

// drawerDim 在抽屉外侧压一层半透明遮罩，把"浮层层级"变成看得见的事实。
// 只压被抽屉盖住的那块会话区：rail、内嵌侧栏、内嵌检查器都不压，
// 它们是"还能点的"区域，压暗会误导成不可用。
func drawerDim(c *ui.Canvas, th *ui.Theme, x, w, h int32) {
	if w <= 0 {
		return
	}
	c.FillRectAlpha(ui.R(x, 0, w, h), th.Tokens().BGOverlay, overlayAlpha)
}

// drawerShade 在抽屉边缘外侧画一条渐变阴影，用最少的绘制交代浮层层级。
// left 为 true 时阴影画在 x 左侧（检查器从右缘滑出用），否则画在右侧。
//
// 用逐列 alpha 而不是"和底色预混"：预混只对纯色底成立，一旦下面是会话正文
// （白卡片、代码块），预混出来的灰条会变成一条明显的脏带。
func drawerShade(c *ui.Canvas, x, y, h int32, left bool) {
	const w = 14
	for i := int32(0); i < w; i++ {
		t := float32(w-i) / float32(w) // 靠边最浓，往外衰减
		col := x + i
		if left {
			col = x - w + i
		}
		c.FillRectAlpha(ui.R(col, y, 1, h), 0x000000, 0.30*t*t)
	}
}

// paintHandles 画两条可拖拽分隔条：静止时只是一条细线，
// 悬停或拖拽时变成强调色竖条，明确告诉用户这里能拉伸。
func (a *App) paintHandles(c *ui.Canvas, H int32) {
	mark := func(x int32, id int) {
		if a.dragMode == id || (a.dragMode == 0 && a.hbHover == id) {
			c.FillRect(ui.R(x-1, 0, 3, H), ui.Alpha(a.th.Bg, a.th.Accent, 0.8))
			// 抓手：拖拽时在两处显示短横，像真实的拖拽手柄
			if a.dragMode == id {
				c.FillRect(ui.R(x-2, H/2-14, 5, 28), a.th.Accent)
			}
			return
		}
		c.VLine(x, 0, H, a.th.BorderSoft)
	}
	if a.showSide && a.sideW > 8 {
		mark(a.railW+a.sideW-1, 1)
	}
	if a.showInsp && a.inspW > 8 {
		mark(a.winW-a.inspW, 2)
	}
}

// handleAt 返回鼠标所在的分隔条：0 无 / 1 侧栏 / 2 检查器。
func (a *App) handleAt(x, y int32) int {
	if a.page != PageChat || a.modal != nil || a.palOpen || a.menu != nil {
		return 0
	}
	if x < 0 || y < 0 || y > a.winH {
		return 0
	}
	if a.showSide && a.sideW > 8 && x > a.railW {
		if bx := a.railW + a.sideW; x >= bx-handleGrab && x <= bx+handleGrab {
			return 1
		}
	}
	if a.showInsp && a.inspW > 8 {
		if bx := a.winW - a.inspW; x >= bx-handleGrab && x <= bx+handleGrab {
			return 2
		}
	}
	return 0
}

// beginDrag 开始拖拽分隔条；400ms 内在同一条上再按一次视为双击，复位默认宽度。
func (a *App) beginDrag(h int, x int32) {
	now := time.Now()
	if a.hbHover == h && !a.hbAt.IsZero() && now.Sub(a.hbAt) < 400*time.Millisecond {
		if h == 1 {
			a.sideWUser = defSideW
		} else {
			a.inspWUser = defInspW
		}
		a.hbAt = time.Time{}
		a.dragMode = 0
		a.layout()
		a.savePanelW()
		return
	}
	a.dragMode = h
	a.dragX0 = x
	if h == 1 {
		a.dragW0 = a.sideWUser
	} else {
		a.dragW0 = a.inspWUser
	}
	a.hbAt = now
	a.hbHover = h
}

// dragPanel 按鼠标位移实时改面板宽度，边界由 clampPanelW 兜住。
func (a *App) dragPanel(x int32) {
	d := x - a.dragX0
	if a.dragMode == 1 {
		a.sideWUser = clampPanelW(a.dragW0+d, minSideW, maxSideW, a.winW)
	} else if a.dragMode == 2 {
		a.inspWUser = clampPanelW(a.dragW0-d, minInspW, maxInspW, a.winW)
	} else {
		return
	}
	a.layout()
}

// savePanelW 把拖拽结果写进配置，下次启动保持同样宽度。
func (a *App) savePanelW() {
	if a.cfg == nil {
		return
	}
	if int32(a.cfg.SideW) == a.sideWUser && int32(a.cfg.InspW) == a.inspWUser {
		return
	}
	a.cfg.SideW = int(a.sideWUser)
	a.cfg.InspW = int(a.inspWUser)
	_ = a.cfg.Save()
}

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

// toggleDrawer 展开/收起侧栏；占位还是浮层由 layout 的自适应判定决定。
func (a *App) toggleDrawer() {
	a.showSide = !a.showSide
	a.layout()
}

// toggleInspector 展开/收起检查器，同样交给自适应判定。
func (a *App) toggleInspector() {
	a.showInsp = !a.showInsp
	a.layout()
	if a.cfg != nil && a.cfg.ShowInsp != a.showInsp {
		a.cfg.ShowInsp = a.showInsp
		_ = a.cfg.Save()
	}
}

// animStep 推进侧栏/检查器折叠动画，返回本帧是否有变化。
func (a *App) animStep() bool {
	moved := false
	step := func(cur *float32, target float32) {
		d := target - *cur
		if d == 0 {
			return
		}
		ad := d
		if ad < 0 {
			ad = -ad
		}
		if ad < 0.02 { // 收尾吸附，避免亚像素区间来回抖动
			*cur = target
		} else {
			*cur += d * 0.34
		}
		moved = true
	}
	ts, ti := float32(0), float32(0)
	if a.showSide {
		ts = 1
	}
	if a.showInsp {
		ti = 1
	}
	step(&a.sideA, ts)
	step(&a.inspA, ti)
	return moved
}

// Paint 绘制一帧。
func (a *App) Paint(c *ui.Canvas) {
	a.beat()
	if a.w != nil {
		state := uint32(win32.TBPF_NOPROGRESS)
		if a.streaming || (a.eng != nil && a.eng.Running()) {
			state = win32.TBPF_INDETERMINATE
		}
		win32.SetTaskbarProgress(a.w.Hwnd(), state)
	}
	if a.perf != nil && a.perf.on {
		t0 := time.Now()
		defer func() { a.observePaint(time.Since(t0)) }()
	}
	if a.ui == nil {
		a.ui = NewUI(c, a.th)
		// 表单初值只在 UI 建立时填一次：过去 initForms 从未被调用，
		// 引导页四个框全是空的；顺便给引导页一个初始焦点，打开即可打字。
		a.initForms()
		if a.page == PageOnboard {
			a.ui.SetFocus("ob.base")
		}
	}
	a.maybePromptPortableUnlock()
	a.ui.c = c
	a.ui.th = a.th
	// primitive/kit 组件从全局 currentTokens 取文字色，而背景来自 a.th。
	// 主题切换后若两者分叉，就会出现"浅底白字"的整页故障。每帧开头统一注入，
	// 让令牌只有 a.th 一个真值来源（原来只在 gallery 分支调用，业务页漏了）。
	ui.SetTokens(a.th.Tokens())
	a.phase += 0.03
	a.ui.Begin(a.mx, a.my, a.phase)
	// 页面/页签切换：命令流会整块换掉，但背景 Clear 命令完全相同，
	// 脏区算法不会重绘"新页面没有画到的旧像素"。这里强制一次全帧。
	key := fmt.Sprintf("%d/%d/%d/%d/%d", a.page, a.autoTab, a.extTab, a.useTab, a.diag.tab)
	if key != a.uiKey {
		a.uiKey = key
		a.forceFull = true
		// 每次进入设置页都从当前配置重铺表单：避免上次未保存的编辑
		// 或者“会话里刚换过 provider”让设置页显示旧数据。
		if a.page == PageSettings {
			a.initForms()
			a.settingsScrl = 0
		}
		if perfProfOn {
			core.Logf("[page] %s", a.pageName())
		}
	}
	if a.forceFull {
		// 面板刚换了形态：这一帧按全屏重放，抹掉上一形态留下的像素碎片。
		a.forceFull = false
		c.InvalidateFrame()
	}

	if a.winW == 0 {
		a.winW, a.winH = c.W(), c.H()
		a.layout()
	}

	c.Clear(a.th.Bg)
	// 组件矩阵页：WIN7H_GALLERY=1 时只画组件与状态，不进业务路径。
	// 用途是把"视觉验收"变成可截图的静态画面（改令牌后逐格核对）。
	if os.Getenv("WIN7H_GALLERY") == "1" {
		ui.DrawGallery(c, a.winW, a.winH)
		return
	}
	// 视觉验收：WIN7H_SHOT_MODAL=diff|plan 用样例数据挂一个模态，
	// 不必接真实 LLM 就能截图核对差分弹窗/方案弹窗的排版（与 GALLERY 同属调试入口）。
	if mode := os.Getenv("WIN7H_SHOT_MODAL"); mode != "" && a.modal == nil {
		a.installShotModal(mode)
	}
	switch a.page {
	case PageOnboard:
		a.paintOnboard(c)
	case PageSettings:
		a.paintSettings(c)
	case PageDiag:
		a.paintDiag(c)
	case PageCompare:
		a.paintCompare(c)
	case PageAuto:
		a.paintAuto(c)
	case PageExt:
		a.paintExt(c)
	case PageUsage:
		a.paintUsage(c)
	case PageSessions:
		a.paintSessionsPage(c)
	case PageWorkspace:
		a.paintWorkspacePage(c)
	case PageCosts:
		a.paintCostsPage(c)
	case PageNotices:
		a.paintNotices(c)
	default:
		a.paintChat(c)
	}
	// 浮层各起一层：canvas 后端的几何与文本是两趟画的，不分层的话
	// 浮层底色压不住下层正文的文字（详见 Canvas.Layer 的注释）。
	if a.palOpen {
		c.Layer()
		a.paintPalette(c)
	}
	if a.histOpen {
		c.Layer()
		a.paintHistory(c)
	}
	if a.menu != nil {
		c.Layer()
		a.paintMenu(c)
	}
	if a.modal != nil {
		c.Layer()
		a.paintModal(c)
	}
	if a.qrVisible() {
		c.Layer()
		a.paintQR(c)
	}
	c.Layer()
	a.paintToast(c)
}

/* ---------------- 主聊天界面 ---------------- */

func (a *App) paintChat(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH
	topH := a.topH()

	// 背景分区
	c.FillRect(ui.R(0, 0, W, H), th.Bg)

	// 侧栏背景（用实际宽度判定，折叠动画期间仍绘制）
	if a.sideW > 0 {
		a.paintSideBG(c, H)
	}
	// 主区：抽屉模式下不占位，起点不含抽屉宽度
	mx := a.railW + a.sideOcc
	c.FillRect(ui.R(mx, 0, a.chatW, H), th.Bg)
	// 右侧抽屉浮在会话区上：顶栏按钮与输入区让开被遮挡的部分，避免被压住点不到
	rightGap := int32(0)
	if a.inspDrawer && a.inspW > 8 {
		rightGap = a.inspW
	}
	cw := a.chatW - rightGap
	// 顶栏用整条主区宽度：右侧抽屉覆盖它也无所谓（抽屉自己有标题与关闭钮），
	// 反而按 cw 排版会把一整排按钮挤到侧栏上，和标题叠字。
	topW := a.chatW

	// 顶栏
	c.FillRect(ui.R(mx, 0, a.chatW, topH), ui.Blend(th.Bg, th.Surface, 0.6))
	c.HLine(mx, topH-1, a.chatW, th.Border)

	// 会话标题
	conv := a.currentConv()
	title := "新对话"
	if conv != nil && conv.Title != "" {
		title = conv.Title
	}
	c.TextFace(mx+20, 14, title, ui.FsMd, 700, th.Text, ui.FaceUI)

	// 状态：呼吸点 + 状态文本
	st := "就绪"
	color := th.Success
	if a.streaming {
		st = "生成中"
		color = th.Accent
	}
	tw := c.MeasureFace(title, ui.FsMd, 700, ui.FaceUI)
	dotX := mx + 30 + tw
	g := 0.5 + 0.5*float32((1+sin(a.phase*2.4))/2)
	c.Dot(dotX, 23, 3, ui.Alpha(th.Bg, color, g))
	c.TextFace(dotX+9, 15, st, ui.FsXs, 400, th.TextMute, ui.FaceUI)
	// 用量摘要常驻顶栏：预算与 token 不该只有发 /status 才看得见。
	if a.eng != nil {
		sw := c.MeasureFace(st, ui.FsXs, 400, ui.FaceUI)
		if line := a.usageSummary(); line != "" {
			ux := dotX + 9 + sw + 10
			limit := mx + topW - 380
			if ux < limit {
				c.TextFace(ux, 15, ellipsis(c, "· "+line, ui.FsXs, 400, limit-ux), ui.FsXs, 400, th.TextMute, ui.FaceUI)
			}
		}
	}

	// 顶栏右侧按钮。
	// 间距收紧到 36，并且全部用矢量图标——原来其中一个按钮写的是汉字"搜"，
	// 和其余图标混排时字重/基线都不一样，一排看过去就是"没对齐"。
	by := int32(11)
	bw := int32(34)
	x := mx + topW - 46
	if u.IconButton("act.insp", ui.R(x, by, bw, 28), "ico:panel", a.showInsp) {
		a.toggleInspector()
	}
	x -= 36
	// 主题按钮不再显示高亮：它是"切一下"，不是"当前选中"，
	// 一直亮着会让人以为这是一组单选按钮。
	if u.IconButton("act.theme", ui.R(x, by, bw, 28), "ico:theme", false) {
		a.toggleTheme()
	}
	x -= 36
	if u.IconButton("act.find", ui.R(x, by, bw, 28), "ico:search", false) {
		a.openPaletteWith("/find ")
	}
	x -= 36
	if u.IconButton("act.new", ui.R(x, by, bw, 28), "ico:plus", false) {
		a.editReset()
		a.newConv()
	}
	// 图标组与工作区之间加一条竖线：不加的话"目录名 + 五个图标"挤成一片，
	// 看不出哪个是"当前上下文"、哪个是"操作"。
	c.VLine(x-12, by+6, 16, th.BorderSoft)

	// 工作区入口：显示本会话实际生效的目录，点开就地切换
	wcR := ui.R(x-158, by+1, 148, 26)
	hoverW := u.Hit("act.workdir", wcR)
	if hoverW {
		c.Round(wcR, 6, ui.Blend(th.Bg, th.Surface, 0.9))
	}
	{
		wd := a.convWorkDir()
		name := "未设置工作区"
		if wd != "" {
			name = filepath.Base(wd)
			if name == "" || name == "." || name == string(filepath.Separator) {
				name = wd
			}
		}
		drawIcon(c, "folder", ui.R(wcR.X+8, wcR.Y+5, 15, 16), th.TextMute)
		tw2 := c.MeasureFace(name, ui.FsXs, 400, ui.FaceUI)
		label := ellipsis(c, name, ui.FsXs, 400, 148-50)
		c.TextFace(wcR.X+28, wcR.Y+6, label, ui.FsXs, 400, th.TextDim, ui.FaceUI)
		_ = tw2
		if a.sessionDirPinned() {
			c.Dot(wcR.X+28+c.MeasureFace(label, ui.FsXs, 400, ui.FaceUI)+7, wcR.Y+12, 2, th.Accent)
		}
		drawIcon(c, "caret-down", ui.R(wcR.Right()-20, wcR.Y+6, 14, 14), th.TextMute)
	}
	if u.Clicked("act.workdir") {
		a.editReset()
		a.openDirMenu(wcR, false)
	}

	// Rail 挪到最后一层画（见下方调用处）。

	// 会话列表：内嵌态现在画（占位、和主区并排）；
	// 抽屉态挪到最后一层画——浮层必须压在会话正文之上，否则正文会穿透到抽屉里。
	if a.sideW > 0 && !a.sideDrawer {
		a.paintConvList(u, H, topH)
	}

	// 消息区
	chatTop := topH
	inputH := a.inputHeight()
	botGap := int32(12)
	if a.dense() {
		botGap = 8
	}
	chatBot := H - inputH - botGap
	// 底部多留 18px：末行内容不贴着输入框边缘，避免长消息被裁成半行。
	clip := ui.R(mx, chatTop, cw, chatBot-chatTop-18)
	// 每次进入都走增量排版：内容没变的消息整段复用，只有流式尾条会真正重排。
	a.layoutSession(c)
	// 排版完成才知道目标消息的纵向位置，跳转在这里消费
	if a.jumpMsg >= 0 {
		if y := a.msgTopOffset(a.jumpMsg); y >= 0 {
			a.scroll = float32(y) - 24
			if a.scroll < 0 {
				a.scroll = 0
			}
			if max := float32(a.contentH - clip.H); max > 0 && a.scroll > max {
				a.scroll = max
			}
		}
		a.jumpMsg = -1
	}
	// 流式结束会改变最后一条消息的高度（Markdown 收尾、工具卡/操作条出现）。
	// 事件处理器当时用的是上一帧 contentH，可能把 scroll 设到新内容高度之外，
	// 结果整屏只剩背景；这里按本帧排版结果统一夹紧，并让 autoScroll 真正到底。
	a.scroll = clampChatScroll(a.scroll, float32(a.contentH), float32(clip.H), a.autoScroll)
	a.drawSession(u, clip)

	// 滚动条
	if a.contentH > clip.H {
		u.Scrollbar(ui.R(mx+cw-8, clip.Y+4, 6, clip.H-8), a.contentH, clip.H, int32(a.scroll))
	}

	// 输入区
	a.paintInput(u, mx, chatBot, cw, H)

	// Inspector：内嵌时贴会话区右侧（先画，让抽屉态可以盖住它）
	if a.inspW > 0 && !a.inspDrawer {
		a.paintInspector(u, mx+cw, a.inspW, H, topH)
	}

	// 分隔条在这里画：它属于"底图"，
	// 放到浮层里会把图层的回写范围拉成"从分隔条到窗口右缘"的一条大矩形，
	// 于是浮层回写时把遮罩区的文字全擦掉（浮层只该擦自己盖住的那块）。
	// 左侧栏放最后画。
	//
	// 它是最靠左、最常被鼠标扫过的控件区，而 hover 会给每个图标多画一条圆角底
	// ——命令数一变，脏区算法就从那条命令一路算到末尾，等于整屏重绘（实测 54 帧里
	// 8 帧全屏、峰值 60ms）。把它挪到最后，差异落在命令流末端，前缀能覆盖绝大部分，
	// 脏区就只剩 rail 那一小块。rail 的区域不与任何内容重叠，z 序放最后是安全的。
	a.paintRail(u, H)

	a.paintHandles(c, H)

	// 抽屉浮层：遮罩 → 阴影 → 面板本体，三步都在最后一层。
	if a.sideW > 0 && a.sideDrawer {
		c.Layer()
		x0 := a.railW + a.sideW
		if x1 := mx + cw; x1 > x0 {
			drawerDim(c, th, x0, x1-x0, H)
		}
		drawerShade(c, a.railW+a.sideW, 0, H, false)
		// 抽屉是浮层：必须先铺自己的底，否则会透出下层正文。
		a.paintSideBG(c, H)
		a.paintConvList(u, H, topH)
		// 抽屉态没有标题栏可点，必须给一个显眼的关闭钮，
		// 否则用户只能靠点空白区猜（而空白区点下去还可能是别的控件）。
		a.ui.IconButton("drw.side.close", ui.R(a.railW+a.sideW-30, 8, 22, 22), "ico:close", false)
	}
	if a.inspW > 0 && a.inspDrawer {
		c.Layer()
		ix := a.winW - a.inspW
		if w := ix - mx; w > 0 {
			drawerDim(c, th, mx, w, H)
		}
		drawerShade(c, ix, 0, H, true)
		a.paintInspector(u, ix, a.inspW, H, topH)
	}
}

// paintSideBG 铺侧栏底色与右缘分隔线。
// 内嵌态在画正文之前铺一次；抽屉态要在浮层层再铺一次（正文会盖掉前面那层）。
func (a *App) paintSideBG(c *ui.Canvas, H int32) {
	if a.sideW <= 0 {
		return
	}
	c.FillRect(ui.R(a.railW, 0, a.sideW, H), a.th.Surface2)
	c.VLine(a.railW+a.sideW-1, 0, H, a.th.Border)
}

// railItem 画一个暖色胶囊 rail 按钮：图标悬停带暖色底，选中态最暖，
// 和 Claude/Raycast 的 rail 一致——外层一颗小圆角方块，而不是裸图标。
func railItem(c *ui.Canvas, u *UI, id, glyph string, x, y int32, active bool) bool {
	th := u.th
	r := ui.R(x, y, 32, 32)
	hover := u.Hit(id, r)
	_ = hover
	hvt := u.hoverT(id)
	bg := uint32(0)
	if active {
		bg = ui.Blend(th.Card, th.AccentSoft, 0.85)
	} else if hvt > 0 {
		bg = ui.Blend(th.Card, th.CardHover, hvt*0.9)
	}
	if bg != 0 {
		c.Round(r, 8, bg)
	}
	if active {
		c.RoundStroke(r, 8, ui.Blend(th.BorderSoft, th.Accent, 0.35))
	}
	kind := glyph
	fg := th.TextDim
	if active {
		fg = th.Accent
	} else if hvt > 0 {
		fg = th.Text
	}
	if ik, ok := iconKind(kind); ok {
		drawIcon(c, ik, ui.R(r.CX()-8, r.CY()-8, 16, 16), fg)
	}
	if u.focus == id {
		c.RoundStroke(r.Inset(-2), 10, th.InputFocus)
	}
	return u.Clicked(id)
}

func (a *App) paintRail(u *UI, H int32) {
	c := u.c
	th := a.th
	c.FillRect(ui.R(0, 0, a.railW, H), th.Surface2)
	c.VLine(a.railW-1, 0, H, th.BorderSoft)

	// Logo（warm 珊瑚）
	DrawLogo(c, 15, 12, 26, th.Accent, th.Surface2)

	// 用暖色胶囊 rail 按钮替代裸图标
	y := int32(58)
	step := int32(40)
	if railItem(c, u, "rail.new", "ico:plus", 12, y, false) {
		a.page = PageChat
		a.newConv()
	}
	y += step
	if railItem(c, u, "rail.side", "ico:menu", 12, y, a.showSide) {
		a.page = PageChat
		a.toggleDrawer()
	}
	y += step
	if railItem(c, u, "rail.diag", "ico:check", 12, y, a.page == PageDiag) {
		a.page = PageDiag
		if len(a.diag.results) == 0 {
			a.runChecksAsync()
		}
	}
	y += step
	if railItem(c, u, "rail.auto", "ico:clock", 12, y, a.page == PageAuto) {
		a.page = PageAuto
	}
	y += step
	if railItem(c, u, "rail.ext", "ico:grid", 12, y, a.page == PageExt) {
		a.page = PageExt
	}
	y += step
	if railItem(c, u, "rail.use", "ico:chart", 12, y, a.page == PageUsage) {
		a.page = PageUsage
	}
	y += step
	if railItem(c, u, "rail.sessions", "ico:star", 12, y, a.page == PageSessions) {
		a.page = PageSessions
		a.sessDirty = true
	}
	y += step
	if railItem(c, u, "rail.workspace", "ico:panel", 12, y, a.page == PageWorkspace) {
		a.page = PageWorkspace
		a.invalidateWorkspace()
	}
	y += step
	if railItem(c, u, "rail.costs", "ico:coin", 12, y, a.page == PageCosts) {
		a.page = PageCosts
	}
	y += step
	if railItem(c, u, "rail.notices", "ico:bell", 12, y, a.page == PageNotices) {
		a.page = PageNotices
	}

	// 左下角：设置入口固定在 rail 底部，和常见桌面应用一致；
	// 上面的业务页再多，设置的位置也不会随列表长度漂移。
	gearY := H - 92
	if gearY < y+8 {
		gearY = y + 8
	}
	if gearY > 64 {
		c.HLine(12, gearY-12, a.railW-24, th.BorderSoft)
	}
	if railItem(c, u, "rail.settings", "ico:gear", 12, gearY, a.page == PageSettings) {
		a.page = PageSettings
		a.settingsScrl = 0
	}
	// 左下角整块都是设置热区：齿轮只有 32px，老人机/触摸屏/高 DPI 下
	// 精确点中小图标并不容易；版本号本身不承载其他动作，可以一起让给设置。
	u.Hit("rail.settings", ui.R(8, H-170, 40, H-30-(H-170)))

	// 底部版本号
	c.TextFace(16, H-30, "v"+core.Version, ui.FsXs, 400, th.TextMute, ui.FaceUI)
}

func (a *App) paintConvList(u *UI, H, topH int32) {
	c := u.c
	th := a.th
	x := a.railW
	// 头部
	c.TextFace(x+16, 14, "会话", ui.FsSm, 700, th.TextDim, ui.FaceUI)
	btn := ui.R(x+a.sideW-82, 8, 70, 24)
	if u.Button("conv.new", "+ 新建", btn, BtnGhost) {
		a.editReset()
		a.newConv()
	}

	// 搜索框：标题与正文都能搜，输入即过滤
	q := u.TextField("conv.q", ui.R(x+8, topH, a.sideW-16, 30), "", "搜索标题或内容…")
	if u.Clicked("conv.q") {
		a.editReset()
		u.SetFocus("conv.q")
	}
	matches := a.searchConvs(q)
	rowH, rowGap := int32(52), int32(56)
	if a.dense() {
		rowH, rowGap = 46, 50
	}
	// 折叠动画收尾段宽度过窄时只留底色，避免文字被挤压变形
	if a.sideW < 64 {
		a.convListH = 0
		return
	}
	y := topH + 34

	listClip := ui.R(x, topH+30, a.sideW, H-topH-70)
	c.Clip(listClip)
	defer c.PopClip()

	if len(matches) == 0 {
		c.TextFace(x+16, topH+52, "没有匹配的会话", ui.FsSm, 400, th.TextMute, ui.FaceUI)
		a.convListH = 0
		return
	}

	for _, m := range matches {
		i := m.idx
		cv := a.convs[i]
		r := ui.R(x+8, y-int32(a.convScrl), a.sideW-16, rowH)
		if r.Y > listClip.Bottom() || r.Bottom() < listClip.Y {
			y += rowGap
			continue
		}
		hover := u.Hit(fmt.Sprintf("conv:%d", i), r)
		bg := uint32(0)
		if i == a.active {
			bg = th.SelectBg
		} else if hover {
			bg = th.CardHover
		}
		if bg != 0 {
			c.Round(r, 8, bg)
		}
		if i == a.active {
			c.Round(ui.R(r.X+2, r.Y+12, 4, r.H-24), 2, th.Accent)
		}

		if i == a.editingID {
			// 就地改名：输入框直接嵌在标题位置，回车提交、Esc 取消
			name := u.TextField("conv.ren", ui.R(r.X+8, r.Y+10, r.W-56, 30), "", "会话名称")
			a.convRename = name
			c.TextFace(r.X+12, r.Y+42, "回车保存 · Esc 取消", ui.FsXs, 400, th.TextMute, ui.FaceUI)
			y += 56
			continue
		}

		t := cv.Title
		if t == "" {
			t = "新对话"
		}
		titleW := r.W - 24
		if hover {
			// 只有鼠标悬停才露出置顶/改名/删除：常态与选中态保持清爽，
			// 选中已经由左侧色条和底色表达，不需要再挂三个图标。
			titleW = r.W - 92
			pinGlyph := "ico:star-off"
			if cv.Pinned {
				pinGlyph = "ico:star"
			}
			if u.IconButton(fmt.Sprintf("conv.pin:%d", i), ui.R(r.Right()-72, r.Y+16, 20, 20), pinGlyph, cv.Pinned) {
			}
			if u.IconButton(fmt.Sprintf("conv.ren:%d", i), ui.R(r.Right()-48, r.Y+16, 20, 20), "ico:edit", false) {
			}
			if u.IconButton(fmt.Sprintf("conv.del:%d", i), ui.R(r.Right()-24, r.Y+16, 20, 20), "ico:close", false) {
			}
		}
		c.TextFace(r.X+12, r.Y+10, ellipsis(c, t, ui.FsBase, 500, titleW), ui.FsBase, 500, th.Text, ui.FaceUI)
		count := len(cv.Msgs)
		if !cv.Loaded {
			count = cv.MsgN
		}
		sub := fmt.Sprintf("%d 条消息 · %s", count, humanWhen(cv.At))
		if m.sip != "" {
			sub = ellipsis(c, m.sip, ui.FsXs, 400, r.W-24)
		}
		c.TextFace(r.X+12, r.Y+30, sub, ui.FsXs, 400, th.TextMute, ui.FaceUI)
		y += 56
	}
	a.convListH = y - (topH + 38)
}

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

func ellipsis(c *ui.Canvas, s string, size, weight, maxW int32) string {
	if c.MeasureFace(s, size, weight, ui.FaceUI) <= maxW {
		return s
	}
	rs := []rune(s)
	for len(rs) > 1 {
		rs = rs[:len(rs)-1]
		if c.MeasureFace(string(rs)+"…", size, weight, ui.FaceUI) <= maxW {
			return string(rs) + "…"
		}
	}
	return string(rs)
}

func humanWhen(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	}
	return t.Format("01-02")
}

func (a *App) inputHeight() int32 { return a.inputHeightBox() }

func (a *App) paintInput(u *UI, x, y, w, H int32) { a.paintInputBox(u, x, y, w, H) }

func (a *App) paintInspector(u *UI, x, w, H, topH int32) {
	c := u.c
	th := a.th
	c.FillRect(ui.R(x, 0, w, H), th.Surface)
	c.VLine(x, 0, H, th.BorderSoft)
	if w < 120 {
		return
	}

	// 检查器以浮层抽屉出现时，标题行右端给一个关闭钮。
	if a.inspDrawer {
		a.ui.IconButton("drw.insp.close", ui.R(x+w-30, 8, 22, 22), "ico:close", false)
	}
	c.TextFace(x+16, 16, "工作区", ui.FsSm, 700, th.TextDim, ui.FaceUI)
	wd := a.convWorkDir()
	if wd == "" {
		wd = core.ExeDir()
	}
	c.TextFace(x+16, 38, ellipsis(c, wd, ui.FsXs, 400, w-32), ui.FsXs, 400, th.TextMute, ui.FaceMono)

	yy := int32(70)
	if a.dense() {
		yy = 56
	}
	c.HLine(x+16, yy-10, w-32, th.BorderSoft)

	// 耗时归因：把 /stats 的核心结论常驻在侧栏，不用每次手打命令。
	if a.eng != nil {
		if modelMs, toolMs := a.eng.ModelToolMs(); modelMs+toolMs > 0 {
			c.TextFace(x+16, yy, "耗时归因", ui.FsSm, 700, th.TextDim, ui.FaceUI)
			yy += 22
			barW := w - 32
			total := float32(modelMs + toolMs)
			mw := int32(float32(barW) * float32(modelMs) / total)
			c.Round(ui.R(x+16, yy, barW, 6), 3, ui.Blend(th.Surface, th.Border, 0.6))
			if mw > 0 {
				c.Round(ui.R(x+16, yy, mw, 6), 3, th.Accent)
			}
			if barW-mw > 0 {
				c.Round(ui.R(x+16+mw, yy, barW-mw, 6), 3, ui.Blend(th.Surface, th.TextMute, 0.55))
			}
			yy += 14
			c.TextFace(x+16, yy, fmt.Sprintf("模型 %.1fs · 工具 %.1fs", float64(modelMs)/1000, float64(toolMs)/1000),
				ui.FsXs, 400, th.TextMute, ui.FaceUI)
			yy += 18
			if tp := a.eng.Throughput(); tp != "" && !strings.HasPrefix(tp, "尚未") {
				c.TextFace(x+16, yy, ellipsis(c, tp, ui.FsXs, 400, barW), ui.FsXs, 400, th.TextMute, ui.FaceUI)
				yy += 18
			}
			if stats := a.eng.StatItems(3); len(stats) > 0 {
				s := stats[0]
				line := fmt.Sprintf("最慢 %s ×%d · %s", ToolTitle(s.Name), s.Calls, durText(s.Total))
				c.TextFace(x+16, yy, ellipsis(c, line, ui.FsXs, 400, barW), ui.FsXs, 400, th.TextMute, ui.FaceUI)
				yy += 18
			}
			yy += 6
			c.HLine(x+16, yy-8, w-32, th.BorderSoft)
		}
	}

	yy = a.paintTaskSide(u, x, w, H, yy)

	step := int32(24)
	if a.dense() {
		step = 21
	}
	c.TextFace(x+16, yy, "本次任务", ui.FsSm, 700, th.TextDim, ui.FaceUI)
	yy += step

	// 最近的工具调用
	var tools []*ToolCard
	for i := len(a.msgs) - 1; i >= 0 && len(tools) < 12; i-- {
		for j := len(a.msgs[i].Tools) - 1; j >= 0 && len(tools) < 12; j-- {
			tools = append([]*ToolCard{a.msgs[i].Tools[j]}, tools...)
		}
	}
	if len(tools) == 0 {
		c.TextFace(x+16, yy, "暂无工具调用", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	}
	for _, t := range tools {
		if yy > H-40 {
			break
		}
		col := th.TextMute
		if !t.Done {
			col = th.Warn
		} else if t.Err {
			col = th.Danger
		} else {
			col = th.Success
		}
		c.Dot(x+20, yy+7, 3, col)
		c.TextFace(x+30, yy, ellipsis(c, ToolTitle(t.Name), ui.FsXs, 400, w-50), ui.FsXs, 400, th.TextDim, ui.FaceUI)
		yy += 22
		s := ArgSummary(t.Name, t.Args)
		if s != "" {
			c.TextFace(x+30, yy-4, ellipsis(c, s, ui.FsXs, 400, w-60), ui.FsXs, 400, th.TextMute, ui.FaceMono)
			yy += 16
		}
	}
}

/* ---------------- 交互 ---------------- */

func (a *App) MouseMove(x, y int32) {
	a.beat()
	a.mx, a.my = x, y
	// kit 页面：只同步坐标给 kit Ctx（hover 依赖它）
	if isKitPage(a.page) {
		if k := a.kitui; k != nil {
			k.Mouse(x, y)
		}
		return
	}
	if a.dragMode != 0 {
		a.dragPanel(x)
		return
	}
	// 首帧 Paint 之前 a.ui 还没建立；WM_MOUSEMOVE 可能先到。
	if a.ui == nil {
		return
	}
	// 主输入区按下后拖动＝拖选，而不是只改悬停态。
	if id, btn := a.ui.Pressed(); id == "input" && btn == 0 {
		if a.inputSel < 0 {
			a.inputSel = a.inputCur
		}
		a.inputCur = a.inputHitRune(x, y)
		return
	}
	// 悬停态随时刷新，供分隔条高亮与左右箭头光标使用
	a.hbHover = a.handleAt(x, y)
}

func (a *App) MouseDown(x, y int32, button int) {
	a.beat()
	if a.ui == nil {
		return
	}
	// kit 页面正文区走 kit 命中；rail 与顶栏（返回按钮）仍走 classic，
	// 否则「← 返回对话」永远收不到按下。
	if a.kitOwnsPoint(x, y) {
		if k := a.kitui; k != nil {
			k.Mouse(x, y)
			k.MouseDown(x, y, button)
		}
		return
	}
	// 记下按下时命中的控件：抬起必须落在同一个控件上、且是左键才派发。
	a.ui.Press(a.ui.HitAt(x, y), button)
	if a.palOpen {
		return
	}
	if a.menu != nil && !a.menuRect.Contains(x, y) {
		// 点浮层外面即收起；抬起时也要吞掉，不能穿透到下层控件。
		a.closeMenu()
		a.suppressClick = true
		return
	}
	if a.modal != nil {
		return
	}
	// 点击输入区即聚焦
	if a.page == PageChat {
		// 分隔条优先命中：贴边点击不该被下面的按钮抢走
		if h := a.handleAt(x, y); h != 0 {
			a.beginDrag(h, x)
			return
		}
		// 抽屉展开时点空白处即收起，符合抽屉的直觉
		if a.sideDrawer && a.sideW > 8 && x > a.railW+a.sideW {
			a.showSide = false
			a.hbHover = 0
			a.layout()
			return
		}
		if a.inspDrawer && a.inspW > 8 && x < a.winW-a.inspW {
			a.showInsp = false
			a.hbHover = 0
			a.layout()
			return
		}
		// 先命中附件缩略图：点一下移除
		for i, rc := range a.chipRects {
			if rc.Contains(x, y) {
				a.removePendingImage(i)
				a.toast("已移除图片", false)
				return
			}
		}
		h := a.inputHeight()
		if y > a.winH-h-8 && x > a.railW+a.sideW {
			a.ui.SetFocus("input")
			if a.ui.HitAt(x, y) == "input" {
				a.inputCur = a.inputHitRune(x, y)
				a.inputSel = -1
			}
		}
	}
}

func (a *App) MouseUp(x, y int32, button int) {
	a.beat()
	if a.ui == nil {
		return
	}
	// kit 页面正文区走 kit 命中；rail 与顶栏交给 classic。
	if a.kitOwnsPoint(x, y) {
		if k := a.kitui; k != nil {
			k.Mouse(x, y)
			k.MouseUp(x, y, button)
		}
		return
	}
	press, pbtn := a.ui.Pressed()
	a.ui.ClearPress()
	if a.dragMode != 0 { // 松手落盘，下次启动保持同一宽度
		a.dragMode = 0
		a.savePanelW()
		return
	}
	if a.suppressClick {
		a.suppressClick = false
		return
	}
	// 右键/中键不派发动作；按下与抬起必须落在同一控件上。
	if button != 0 || pbtn != 0 {
		return
	}
	if a.qrVisible() {
		// 二维码面板是"看一眼就走"的浮层，点哪儿都关，不设关闭按钮。
		a.closeQR()
		return
	}
	if a.palOpen {
		a.palClick(x, y)
		return
	}
	if a.histOpen {
		a.histClick(x, y)
		return
	}
	if a.menu != nil {
		a.menuClick(x, y)
		return
	}
	if a.modal != nil {
		a.modalClick(x, y)
		return
	}
	id := a.ui.HandleClick(x, y)
	if id == "" || (press != "" && press != id) {
		return
	}
	a.dispatchClick(id)
}

func (a *App) dispatchClick(id string) {
	switch {
	case strings.HasPrefix(id, "chip:"):
		idx := 0
		fmt.Sscanf(id, "chip:%d", &idx)
		chips := []string{
			"帮我看看这个目录的项目结构",
			"用 Go 写一个命令行待办工具并编译",
			"分析这里的报错并修复",
			"给这个项目补一份 README",
		}
		if idx >= 0 && idx < len(chips) {
			a.input = []rune(chips[idx])
			a.inputCur = len(a.input)
			a.send()
		}
	case strings.HasPrefix(id, "conv:"):
		idx := 0
		fmt.Sscanf(id, "conv:%d", &idx)
		if idx >= 0 && idx < len(a.convs) {
			if cur := a.currentConv(); cur != nil {
				cur.Scroll = a.scroll
			}
			a.saveConv()
			a.active = idx
			a.hlMsg = -1
			a.loadConv()
		}
	case strings.HasPrefix(id, "hit:"):
		var mi, hi int
		fmt.Sscanf(id, "hit:%d:%d", &mi, &hi)
		if mi < len(a.msgs) && hi < len(a.msgs[mi].Hits) {
			a.openHitView(a.msgs[mi].Hits[hi])
		}
	case strings.HasPrefix(id, "path:"):
		idx := 0
		fmt.Sscanf(id, "path:%d", &idx)
		if idx >= 0 && idx < len(a.msgLinks) {
			link := a.msgLinks[idx]
			a.openPathAtLine(link.Abs, link.From)
		}
	case id == "drw.side.close":
		a.showSide = false
		a.hbHover = 0
		a.layout()
	case id == "drw.insp.close":
		a.showInsp = false
		a.hbHover = 0
		a.layout()
	case strings.HasPrefix(id, "tool:"):
		var mi, ti int
		fmt.Sscanf(id, "tool:%d:%d", &mi, &ti)
		if mi < len(a.msgs) && ti < len(a.msgs[mi].Tools) {
			a.msgs[mi].Tools[ti].Expanded = !a.msgs[mi].Tools[ti].Expanded
			a.ver++
		}
	case strings.HasPrefix(id, "conv.pin:"):
		idx := 0
		fmt.Sscanf(id, "conv.pin:%d", &idx)
		a.togglePin(idx)
	case strings.HasPrefix(id, "conv.ren:"):
		idx := 0
		fmt.Sscanf(id, "conv.ren:%d", &idx)
		a.startRename(idx)
	case strings.HasPrefix(id, "conv.del:"):
		idx := 0
		fmt.Sscanf(id, "conv.del:%d", &idx)
		a.requestDeleteConv(idx)
	case id == "input.img":
		// 一个按钮吃两种来源：剪贴板有图就贴，没有就弹选图框
		if !a.pasteClipboardImage() {
			a.pickImages()
		}
	case id == "input.snip":
		a.startSnip()
	case id == "input.cmd":
		a.openPaletteWith("/")
	case strings.HasPrefix(id, "msgact.copy:"):
		idx := 0
		fmt.Sscanf(id, "msgact.copy:%d", &idx)
		a.copyMsg(idx)
	case strings.HasPrefix(id, "msgact.retry:"):
		idx := 0
		fmt.Sscanf(id, "msgact.retry:%d", &idx)
		a.retryFrom(idx)
	case strings.HasPrefix(id, "msgact.edit:"):
		idx := 0
		fmt.Sscanf(id, "msgact.edit:%d", &idx)
		a.editMsg(idx)
	case strings.HasPrefix(id, "toolcopy:"):
		var mi, ti int
		fmt.Sscanf(id, "toolcopy:%d:%d", &mi, &ti)
		a.copyToolOutput(mi, ti)
	case strings.HasPrefix(id, "codeact.copy:"):
		var mi, bi int
		fmt.Sscanf(id, "codeact.copy:%d:%d", &mi, &bi)
		a.copyCodeBlock(mi, bi)
	case strings.HasPrefix(id, "codeact.save:"):
		var mi, bi int
		fmt.Sscanf(id, "codeact.save:%d:%d", &mi, &bi)
		a.saveCodeBlock(mi, bi)
	}
}

func (a *App) MouseWheel(x, y int32, delta int32) {
	a.beat()
	if a.perf != nil && a.perf.on {
		t0 := time.Now()
		defer func() { a.observeWheel(time.Since(t0)) }()
	}
	// 原始增量换算成整格：高精度滚轮的小增量会先积累，攒够一格再动。
	n := a.wheelNotches(delta)
	if n == 0 {
		return
	}
	step := -n // 向下滚为正

	// 富差分弹窗内容可能超出一屏，滚轮在这里独立滚动（单位是像素）。
	if a.modal != nil && a.modal.Kind == modalDiff {
		a.modal.Scroll += float32(step) * 54
		if a.modal.Scroll < 0 {
			a.modal.Scroll = 0
		}
		if a.modal.ScrollMax > 0 && a.modal.Scroll > a.modal.ScrollMax {
			a.modal.Scroll = a.modal.ScrollMax
		}
		a.Redraw()
		return
	}
	if a.modal != nil && a.modal.Kind == modalPlan {
		a.modal.Scroll += float32(step) * 54
		if a.modal.Scroll < 0 {
			a.modal.Scroll = 0
		}
		if a.modal.ScrollMax > 0 && a.modal.Scroll > a.modal.ScrollMax {
			a.modal.Scroll = a.modal.ScrollMax
		}
		a.Redraw()
		return
	}
	// kit 页面：先确认指针在内容区，避免在 rail / 顶栏上滚轮误滚整页。
	if isKitPage(a.page) {
		if !a.kitOwnsPoint(x, y) {
			return
		}
		if k := a.kitui; k != nil {
			for _, key := range a.kitScrollKeys() {
				kit.ScrollBy(k, key, step*64)
			}
		}
		return
	}
	if a.page == PageDiag {
		if y < 60 {
			return
		}
		view := float32(a.winH) - 116 - 16
		max := float32(a.diag.height) - view
		if max < 0 {
			max = 0
		}
		a.diag.scroll += float32(step) * 64
		if a.diag.scroll < 0 {
			a.diag.scroll = 0
		}
		if a.diag.scroll > max {
			a.diag.scroll = max
		}
		return
	}
	if a.page == PageSettings {
		if y < 108 {
			return
		}
		max := float32(a.settingsH) - (float32(a.winH) - 144)
		if max < 0 {
			max = 0
		}
		a.settingsScrl += float32(step) * 64
		if a.settingsScrl < 0 {
			a.settingsScrl = 0
		}
		if a.settingsScrl > max {
			a.settingsScrl = max
		}
		return
	}
	if a.page != PageChat {
		return
	}
	if a.modal != nil {
		return
	}
	if y < 46 {
		return
	}
	// 输入卡内有溢出内容时，滚轮优先滚动输入（否则滚动对话）
	if a.inputRect.W > 0 && y >= a.inputRect.Y {
		line := step
		lines := a.wrapInput()
		vis := a.inputVisibleLines()
		if len(lines) > vis {
			scroll := a.inputScroll + line
			if scroll < 0 {
				scroll = 0
			}
			if max := int32(len(lines) - vis); scroll > max {
				scroll = max
			}
			a.inputScroll = scroll
			a.inputPinScroll = true
			return
		}
	}
	// 侧栏会话列表滚动（搜索过滤后条目变少，限位按实际内容算）
	if a.sideW > 0 && x < a.railW+a.sideW {
		a.convScrl += float32(step) * 56
		if a.convScrl < 0 {
			a.convScrl = 0
		}
		max := float32(a.convListH) - (float32(a.winH) - 116)
		if max < 0 {
			max = 0
		}
		if a.convScrl > max {
			a.convScrl = max
		}
		return
	}
	a.scroll += float32(step) * 64
	if a.scroll < 0 {
		a.scroll = 0
	}
	max := float32(a.contentH) - (float32(a.winH) - 46 - float32(a.inputHeight()) - 12)
	if max < 0 {
		max = 0
	}
	if a.scroll > max {
		a.scroll = max
	}
	a.autoScroll = a.scroll >= max-4
}

// kitScrollKeys 返回当前页面真正需要滚动的 Scroll 容器 key。
// 滚轮与 PageUp/PageDown/Home/End 共用这一份映射，避免两处漏配。
func (a *App) kitScrollKeys() []string {
	switch a.page {
	case PageSessions:
		return []string{"s:main"}
	case PageWorkspace:
		return []string{"w:main"}
	case PageCosts:
		return []string{"cost:main"}
	case PageCompare:
		return []string{"cmp:当前会话", "cmp:对照会话"}
	case PageAuto:
		switch a.autoTab {
		case autoTabQueue:
			return []string{"auto:queue"}
		case autoTabSched:
			return []string{"auto:sched"}
		case autoTabBg:
			return []string{"auto:bg"}
		default:
			return []string{"auto:watch"}
		}
	case PageUsage:
		return []string{"use:main"}
	case PageExt:
		switch a.extTab {
		case extTabDevice:
			return []string{"ext:device"}
		case extTabRemote:
			return []string{"ext:remote"}
		case extTabAgents:
			return []string{"ext:agents"}
		case extTabSkills:
			return []string{"ext:skills"}
		case extTabMCP:
			return []string{"ext:mcp"}
		case extTabMem:
			return []string{"ext:mem"}
		}
	case PageNotices:
		return []string{"notice:main"}
	case PageDiag:
		switch a.diag.tab {
		case diagTabChecks:
			return []string{"diag:checks"}
		case diagTabLogs:
			return []string{"diag:logs"}
		case diagTabEndToEnd:
			return []string{"diag:e2e"}
		case diagTabEval:
			return []string{"diag:eval"}
		case diagTabCrash:
			return []string{"diag:crash"}
		}
	}
	return nil
}

func (a *App) KeyDown(vk int32) bool {
	a.beat()
	if a.ui == nil {
		return false
	}
	if a.modal != nil && a.modal.Kind == modalPassword {
		return a.passwordKey(vk)
	}
	if isKitPage(a.page) {
		if vk == win32.VK_ESCAPE {
			a.page = PageChat
			return true
		}
		// Tab / Shift+Tab：在 kit 控件（按钮/输入框）之间循环焦点
		if vk == win32.VK_TAB {
			if k := a.kitui; k != nil {
				k.MoveFocus(win32.GetKeyState(win32.VK_SHIFT))
			}
			return true
		}
		// 所有 kit 页：PageUp/PageDown 翻页，Home/End 到顶/到底。
		// 并列查看页额外支持 ↑↓ 微调两栏。
		if k := a.kitui; k != nil {
			keys := a.kitScrollKeys()
			switch vk {
			case win32.VK_PRIOR:
				for _, key := range keys {
					kit.ScrollPage(k, key, -1)
				}
				return true
			case win32.VK_NEXT:
				for _, key := range keys {
					kit.ScrollPage(k, key, 1)
				}
				return true
			case win32.VK_HOME:
				for _, key := range keys {
					kit.ScrollHome(k, key)
				}
				return true
			case win32.VK_END:
				for _, key := range keys {
					kit.ScrollEnd(k, key)
				}
				return true
			case win32.VK_UP, win32.VK_DOWN:
				if a.page == PageCompare {
					delta := int32(40)
					if vk == win32.VK_UP {
						delta = -40
					}
					for _, key := range keys {
						kit.ScrollBy(k, key, delta)
					}
					return true
				}
			}
		}
		if k := a.kitui; k != nil {
			if foc := k.Focus(); foc != "" {
				switch vk {
				case win32.VK_BACK:
					kit.InputKey(k, foc, 0, true)
					return true
				case win32.VK_RETURN:
					// Enter：激活聚焦的按钮（ kit 按钮/输入框都注册了命中）
					k.DispatchClick(foc)
					return true
				}
			}
		}
		return vk == win32.VK_RETURN || vk == win32.VK_ESCAPE
	}
	if vk == win32.VK_PRIOR || vk == win32.VK_NEXT {
		step := float32(1)
		if vk == win32.VK_PRIOR {
			step = -1
		}
		switch a.page {
		case PageChat:
			if a.ui.Focus() != "input" && a.modal == nil {
				view := float32(a.winH - 46 - a.inputHeight() - 12)
				max := float32(a.contentH) - view
				if max < 0 {
					max = 0
				}
				a.scroll += step * view * 0.85
				if a.scroll < 0 {
					a.scroll = 0
				}
				if a.scroll > max {
					a.scroll = max
				}
				a.autoScroll = a.scroll >= max-4
				return true
			}
		case PageSettings:
			view := float32(a.winH - 144)
			max := float32(a.settingsH) - view
			if max < 0 {
				max = 0
			}
			a.settingsScrl += step * view * 0.85
			if a.settingsScrl < 0 {
				a.settingsScrl = 0
			}
			if a.settingsScrl > max {
				a.settingsScrl = max
			}
			return true
		}
	}
	if a.editingID >= 0 {
		switch vk {
		case win32.VK_ESCAPE:
			a.commitRename(false)
			return true
		case win32.VK_RETURN:
			a.commitRename(true)
			return true
		}
		if a.fieldKey(vk) {
			return true
		}
		return true // 改名期间吞掉其余按键，避免误触发发送/滚动
	}
	if a.qrVisible() {
		switch vk {
		case win32.VK_ESCAPE, win32.VK_RETURN, win32.VK_SPACE:
			a.closeQR()
		}
		return true
	}
	if a.palOpen {
		return a.palKey(vk)
	}
	if a.histOpen {
		return a.histKey(vk)
	}
	if a.menu != nil {
		return a.menuKey(vk)
	}
	if a.modal != nil {
		// 富差分与方案弹窗的默认键都是"安全侧"：Esc/回车默认取消或拒绝，
		// 必须 Tab 切到执行按钮后再回车，避免长任务里无脑回车误批。
		if a.modal.Kind == modalDiff {
			switch vk {
			case win32.VK_ESCAPE:
				a.modalCancelDiff()
			case win32.VK_TAB, win32.VK_LEFT, win32.VK_RIGHT:
				a.modalOKFocused = !a.modalOKFocused
			case win32.VK_RETURN, win32.VK_SPACE:
				if a.modalOKFocused {
					a.modalAnswerDiff(false)
				} else {
					a.modalCancelDiff()
				}
			}
			return true
		}
		if a.modal.Kind == modalPlan {
			switch vk {
			case win32.VK_ESCAPE:
				a.modalAnswerPlan("reject", "")
			case win32.VK_TAB, win32.VK_LEFT, win32.VK_RIGHT:
				a.modalOKFocused = !a.modalOKFocused
			case 'M', 'm':
				a.modalAnswerPlan("modify", a.modal.Plan)
			case win32.VK_RETURN, win32.VK_SPACE:
				if a.modalOKFocused {
					a.modalAnswerPlan("approve", "")
				} else {
					a.modalAnswerPlan("reject", "")
				}
			}
			return true
		}
		switch vk {
		case win32.VK_ESCAPE:
			a.modalAnswer(false)
			return true
		case win32.VK_TAB, win32.VK_LEFT, win32.VK_RIGHT:
			a.modalOKFocused = !a.modalOKFocused
			return true
		case win32.VK_RETURN, win32.VK_SPACE:
			a.modalAnswer(a.modalOKFocused)
			return true
		}
		return true
	}

	if len(a.comp) > 0 && a.ui.Focus() == "input" && a.page == PageChat {
		switch vk {
		case win32.VK_ESCAPE:
			a.comp = nil
			return true
		case win32.VK_UP:
			a.compIdx--
			if a.compIdx < 0 {
				a.compIdx = len(a.comp) - 1
			}
			return true
		case win32.VK_DOWN:
			a.compIdx++
			if a.compIdx >= len(a.comp) {
				a.compIdx = 0
			}
			return true
		case win32.VK_TAB:
			if a.acceptComplete() {
				return true
			}
		case win32.VK_RETURN:
			if a.acceptComplete() {
				return true
			}
		}
	}

	ctrl := win32.GetKeyState(win32.VK_CONTROL)
	shift := win32.GetKeyState(win32.VK_SHIFT)
	alt := win32.GetKeyState(win32.VK_MENU)

	// Ctrl+, 打开设置（0xBC = VK_OEM_COMMA）。桌面应用通用快捷键，
	// 也给“设置入口在左下角”提供一条不依赖鼠标的路径。
	if ctrl && a.cfg != nil && a.cfg.Onboarded && vk == 0xBC {
		a.page = PageSettings
		a.settingsScrl = 0
		return true
	}

	// 组合键优先于输入框编辑：Ctrl+R 即使焦点在输入框也要打开历史搜索。
	if a.keyMatches("historySearch", vk, ctrl, shift, alt) {
		a.openHistory()
		return true
	}

	// Tab / Shift+Tab 在表单与按钮之间循环焦点；补全候选打开时 Tab 仍归补全。
	if vk == win32.VK_TAB && !(len(a.comp) > 0 && a.ui.Focus() == "input" && a.page == PageChat) {
		a.moveFocus(shift)
		return true
	}

	// 侧栏搜索框等非聊天输入框的编辑键交给字段自己处理。
	if a.ui.Focus() != "input" && a.ui.Focus() != "" && a.fieldKey(vk) {
		return true
	}

	// 聊天主输入：选区、剪贴、撤销、跨行光标统一在这里处理。
	if a.ui.Focus() == "input" && a.page == PageChat && a.editKey(vk, ctrl, shift) {
		return true
	}

	if a.keyMatches("palette", vk, ctrl, shift, alt) {
		a.openPalette()
		return true
	}
	if a.keyMatches("sidebar", vk, ctrl, shift, alt) {
		a.toggleDrawer()
		return true
	}
	if a.keyMatches("inspector", vk, ctrl, shift, alt) {
		a.toggleInspector()
		return true
	}
	if a.keyMatches("toggleFold", vk, ctrl, shift, alt) {
		a.cfg.NoFoldTools = !a.cfg.NoFoldTools
		a.foldOpen = nil
		_ = a.cfg.Save()
		a.layout()
		if a.cfg.NoFoldTools {
			a.toast("工具卡：逐条显示", false)
		} else {
			a.toast("工具卡：按组折叠", false)
		}
		return true
	}
	if a.keyMatches("find", vk, ctrl, shift, alt) {
		if a.ui.Focus() == "conv.q" {
			a.ui.SetFocus("input")
		}
		a.openPaletteWith("/find ")
		return true
	}
	if a.ui.Focus() == "conv.q" && (vk == win32.VK_RETURN || vk == win32.VK_ESCAPE) {
		if vk == win32.VK_ESCAPE {
			a.ui.SetField("conv.q", "")
		}
		a.ui.SetFocus("input")
		return true
	}

	switch vk {
	case win32.VK_ESCAPE:
		if a.streaming {
			a.stopGeneration()
			return true
		}
		return false
	case win32.VK_RETURN:
		if a.page != PageChat {
			a.pageDefaultAction()
			return true
		}
		if ctrl || !a.cfg.SendOnEnter || shift {
			a.insertInput("\n")
			return true
		}
		a.send()
		return true
	case win32.VK_C:
		if ctrl {
			if lo, hi := a.selRange(); lo >= 0 {
				win32.SetClipboardText(string(a.input[lo:hi]))
				return true
			}
			txt := string(a.input)
			if txt != "" {
				win32.SetClipboardText(txt)
				return true
			}
			if n := len(a.msgs); n > 0 && a.msgs[n-1].Role == "assistant" {
				win32.SetClipboardText(a.msgs[n-1].Text)
				a.toast("已复制最后一条回复", false)
				return true
			}
		}
	case win32.VK_V:
		if ctrl {
			if a.pasteClipboardImage() {
				return true
			}
			if txt := win32.GetClipboardText(); txt != "" {
				a.insertInput(txt)
			}
			return true
		}
	case 0x4E:
		if ctrl {
			a.newConv()
			return true
		}
	case 0x52: // Ctrl+R 的兜底：组合键解析失败时仍按默认打开历史
		if ctrl && !shift && !alt {
			a.openHistory()
			return true
		}
	case win32.VK_PRIOR:
		a.scroll -= 300
		if a.scroll < 0 {
			a.scroll = 0
		}
		return true
	case win32.VK_NEXT:
		a.scroll += 300
		max := float32(a.contentH) - (float32(a.winH) - 46 - float32(a.inputHeight()) - 12)
		if a.scroll > max {
			a.scroll = max
		}
		return true
	}
	return false
}

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

// editKey 处理主输入框的编辑键；返回 true 表示已消费。
func (a *App) editKey(vk int32, ctrl, shift bool) bool {
	a.inputPinScroll = false // 编辑即跟随光标
	n := len(a.input)
	lines := a.inputLineBounds()
	lineOf := func(pos int) (int, int, int) {
		for i, l := range lines {
			if pos >= l.start && pos <= l.end {
				return i, l.start, l.end
			}
		}
		l := lines[len(lines)-1]
		return len(lines) - 1, l.start, l.end
	}
	setPos := func(p int) {
		if p < 0 {
			p = 0
		}
		if p > n {
			p = n
		}
		if shift {
			if a.inputSel < 0 {
				a.inputSel = a.inputCur
			}
			a.inputCur = p
		} else {
			a.inputSel = -1
			a.inputCur = p
		}
	}
	switch {
	case ctrl && (vk == 'A' || vk == 'a'):
		a.inputSel, a.inputCur = 0, n
		return true
	case ctrl && (vk == 'Z' || vk == 'z'):
		if len(a.inputUndo) > 0 {
			last := a.inputUndo[len(a.inputUndo)-1]
			a.inputUndo = a.inputUndo[:len(a.inputUndo)-1]
			a.input = append([]rune(nil), last...)
			a.inputCur, a.inputSel = len(a.input), -1
			a.refreshComplete()
		}
		return true
	case ctrl && (vk == 'X' || vk == 'x'):
		if lo, hi := a.selRange(); lo >= 0 {
			win32.SetClipboardText(string(a.input[lo:hi]))
			a.pushInputUndo()
			a.input = append(a.input[:lo], a.input[hi:]...)
			a.inputCur, a.inputSel = lo, -1
			a.refreshComplete()
			return true
		}
		return false
	case ctrl && (vk == 'C' || vk == 'c'):
		if lo, hi := a.selRange(); lo >= 0 {
			win32.SetClipboardText(string(a.input[lo:hi]))
			return true
		}
		return false
	case vk == win32.VK_BACK || vk == win32.VK_DELETE:
		if lo, hi := a.selRange(); lo >= 0 {
			a.pushInputUndo()
			a.input = append(a.input[:lo], a.input[hi:]...)
			a.inputCur, a.inputSel = lo, -1
		} else if vk == win32.VK_BACK && a.inputCur > 0 {
			a.pushInputUndo()
			a.input = append(a.input[:a.inputCur-1], a.input[a.inputCur:]...)
			a.inputCur--
		} else if vk == win32.VK_DELETE && a.inputCur < n {
			a.pushInputUndo()
			a.input = append(a.input[:a.inputCur], a.input[a.inputCur+1:]...)
		}
		a.refreshComplete()
		return true
	case vk == win32.VK_LEFT:
		setPos(a.inputCur - 1)
		return true
	case vk == win32.VK_RIGHT:
		setPos(a.inputCur + 1)
		return true
	case vk == win32.VK_HOME:
		_, st, _ := lineOf(a.inputCur)
		setPos(st)
		return true
	case vk == win32.VK_END:
		_, _, en := lineOf(a.inputCur)
		setPos(en)
		return true
	case vk == win32.VK_UP || vk == win32.VK_DOWN:
		row, st, _ := lineOf(a.inputCur)
		col := a.inputCur - st
		if vk == win32.VK_UP {
			row--
		} else {
			row++
		}
		if row < 0 {
			setPos(0)
			return true
		}
		if row >= len(lines) {
			setPos(n)
			return true
		}
		l := lines[row]
		p := l.start + col
		if p > l.end {
			p = l.end
		}
		setPos(p)
		return true
	}
	return false
}

// moveFocus 在页面内的输入框与默认按钮之间循环焦点。
func (a *App) moveFocus(shift bool) {
	order := []string{"input"}
	switch a.page {
	case PageOnboard:
		order = []string{"ob.base", "ob.key", "ob.model", "ob.dir", "ob.start"}
	case PageSettings:
		switch a.settingsTab {
		case 0:
			order = []string{"set.base", "set.key", "set.model", "set.dir"}
		case 3:
			order = []string{"set.proxyurl", "set.tlsca"}
		case 4:
			order = []string{"set.maxcost", "set.budget", "set.rounds", "set.ctxwin", "set.subto", "set.pricein", "set.priceout"}
		case 5:
			order = []string{"hook.event", "hook.match", "hook.cmd", "set.temp", "set.maxtok", "set.gitname", "set.gitmail"}
		default:
			order = nil
		}
	case PageChat:
		order = []string{"conv.q", "input"}
	}
	if len(order) == 0 {
		return
	}
	cur := a.ui.Focus()
	idx := -1
	for i, id := range order {
		if id == cur {
			idx = i
			break
		}
	}
	step := 1
	if shift {
		step = -1
	}
	next := 0
	if idx >= 0 {
		next = (idx + step + len(order)) % len(order)
	} else if shift {
		next = len(order) - 1
	}
	a.ui.SetFocus(order[next])
}

// pageDefaultAction 让 Enter 在非聊天页触发该页的主按钮。
func (a *App) pageDefaultAction() {
	switch a.page {
	case PageOnboard:
		a.finishOnboard()
	case PageSettings:
		a.saveSettings()
	case PageDiag, PageCompare, PageAuto, PageExt, PageUsage, PageNotices:
		a.page = PageChat
	}
}

func (a *App) fieldKey(vk int32) bool {
	id := a.ui.Focus()
	if id == "" {
		return false
	}
	f := a.ui.fields[id]
	if f == nil {
		return false
	}
	ctrl := win32.GetKeyState(win32.VK_CONTROL)
	switch vk {
	case win32.VK_BACK:
		f.Backspace()
		return true
	case win32.VK_DELETE:
		f.Delete()
		return true
	case win32.VK_LEFT:
		if f.Cur > 0 {
			f.Cur--
		}
		return true
	case win32.VK_RIGHT:
		if f.Cur < len(f.Text) {
			f.Cur++
		}
		return true
	case win32.VK_HOME:
		f.Cur = 0
		return true
	case win32.VK_END:
		f.Cur = len(f.Text)
		return true
	case win32.VK_V:
		if ctrl {
			if txt := win32.GetClipboardText(); txt != "" {
				f.Insert(txt)
			}
			return true
		}
	}
	return false
}

func (a *App) Char(ch rune) {
	a.beat()
	if a.ui == nil {
		return
	}
	if a.modal != nil && a.modal.Kind == modalPassword {
		if id := a.ui.Focus(); id == "modal.pwd1" || id == "modal.pwd2" {
			if f := a.ui.fields[id]; f != nil && ch >= 32 {
				f.Insert(string(ch))
			}
		}
		return
	}
	a.inputPinScroll = false // 编辑即跟随光标
	// kit 页面：聚焦到 kit 输入框就走 kit 的输入路径
	if isKitPage(a.page) {
		if k := a.kitui; k != nil {
			if foc := k.Focus(); foc != "" {
				kit.InputKey(k, foc, ch, false)
			}
		}
		return
	}
	if a.palOpen {
		a.palType(ch)
		return
	}
	if a.histOpen {
		a.histType(ch)
		return
	}
	if a.menu != nil {
		if ch == 27 {
			a.closeMenu()
		}
		return
	}
	if a.modal != nil {
		return
	}
	if a.page != PageChat {
		f := a.ui.fields[a.ui.Focus()]
		if f != nil {
			f.Insert(string(ch))
		}
		return
	}
	if ch < 32 {
		return
	}
	if id := a.ui.Focus(); id != "input" {
		if f := a.ui.fields[id]; f != nil {
			f.Insert(string(ch))
			return
		}
	}
	a.insertInput(string(ch))
}

func (a *App) insertInput(s string) {
	a.pushInputUndo()
	if lo, hi := a.selRange(); lo >= 0 {
		a.input = append(a.input[:lo], a.input[hi:]...)
		a.inputCur = lo
		a.inputSel = -1
	}
	rs := []rune(s)
	out := make([]rune, 0, len(a.input)+len(rs))
	out = append(out, a.input[:a.inputCur]...)
	out = append(out, rs...)
	out = append(out, a.input[a.inputCur:]...)
	a.input = out
	a.inputCur += len(rs)
	a.refreshComplete()
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
	if fn != nil {
		a.uiQueue = append(a.uiQueue, fn)
	}
}

// runUIQueue 在 Timer 里执行 Paint 期间排队的慢动作（保存、系统对话框等）。
// 执行前先清空切片：动作里再 queueUI 的会落到下一轮，避免同一帧无限递归。
func (a *App) runUIQueue() {
	if len(a.uiQueue) == 0 {
		return
	}
	q := a.uiQueue
	a.uiQueue = nil
	for _, fn := range q {
		fn()
	}
}

func (a *App) Timer() bool {
	a.beat()
	a.runUIQueue()
	if a.perf != nil && a.perf.on {
		t0 := time.Now()
		defer func() { a.observeTimer(time.Since(t0)) }()
	}
	now := time.Now()
	dt := now.Sub(a.lastTick)
	a.lastTick = now
	_ = dt

	// 按需重绘：交互/流式/浮层保持 33fps；完全静止时降到 4fps。
	// 输入事件本身会 Invalidate，不会牺牲按键的即时响应。
	anim := a.streaming || a.tst != nil || a.palOpen || a.histOpen || a.menu != nil || a.modal != nil ||
		a.qrVisible() || len(a.comp) > 0 || a.sideDrawer || a.inspDrawer ||
		(a.ui != nil && a.ui.Animating())
	need := anim || now.Sub(a.lastIdlePaint) >= 250*time.Millisecond
	if need {
		a.lastIdlePaint = now
	}

	// v1.27：定时任务、手机指令、目录守护、更新提示都在主循环里落地，
	// 后台线程只投递消息，不碰界面状态。
	a.drainWatch()
	a.drainUpdate()
	a.tickV127(now)
	a.tickV128(now)
	a.tickV129(now)
	a.drainConvLoads()
	a.drainHydration()
	select {
	case <-a.compScanDone:
		if _, _, ok := a.atToken(); ok {
			a.refreshComplete()
			a.Redraw()
		}
	default:
	}

	// 设置页的“测试连接 / 拉取模型”结果。历史上 testCh 只写不读，
	// 点一次测试后按钮会永远卡在“测试中…”，这里一并补上消费端。
	select {
	case res := <-a.testCh:
		a.testing = false
		a.testMsg = res.msg
		a.testOK = &res.ok
		a.Redraw()
	default:
	}
	select {
	case res := <-a.modelsCh:
		a.applyModelList(res)
	default:
	}
	select {
	case res := <-a.doctorCh:
		a.doctorBusy = false
		a.sysNote(res.msg)
		if res.ok {
			a.toast("端点诊断完成", false)
		} else {
			a.toast("端点诊断发现问题，报告已写入对话", true)
		}
	default:
	}

	// 弹窗请求：已有弹窗时排队，绝不能直接覆盖（旧 Resp 会永远没人回答）。
	select {
	case req := <-a.confirmCh:
		a.pendingConfirms = append(a.pendingConfirms, req)
	default:
	}
	a.drainDecisionRequests()
	if a.remoteClose.Swap(false) && a.modal != nil && a.modal.Confirm {
		// 手机已经答过了：把电脑端确认框撤掉，不再等鼠标点。
		a.modal = nil
	}

	// 引擎事件
	for {
		select {
		case ev := <-a.evCh:
			a.onEvent(ev)
		default:
			goto drained
		}
	}
drained:
	if a.tst != nil && now.After(a.tst.Until) {
		a.tst = nil
	}
	if a.animInit && a.animStep() {
		a.layout()
	}
	return need
}

func (a *App) Close() bool {
	if a.shutdownDone.Load() {
		return true
	}
	if a.perf != nil {
		a.perf.closing.Store(true)
	}
	if a.streaming && a.eng != nil {
		// 生成中直接销毁窗口会把正在跑的工具/流式连接一起带走，表现就是
		// “点关闭后闪退”。先拦下，让用户点停止或等本轮结束。
		a.toast("生成中不能退出：先点停止，或等这一轮结束", true)
		return false
	}
	a.saveConv()
	if a.saver != nil {
		if !a.saver.stop(8 * time.Second) {
			core.Logf("退出时会话队列未在 8 秒内写完，可能有最后一小段输出未落盘")
		}
	}
	_ = a.cfg.Save()
	mcp.Shutdown()
	a.shutdownDone.Store(true)
	return true
}

func (a *App) onEvent(ev agent.Event) {
	a.evMu.Lock()
	defer a.evMu.Unlock()
	conv := a.currentConv()
	m := a.streamMsg()
	switch ev.Kind {
	case "content":
		if m == nil {
			m = a.newAssistantMsg()
		}
		m.Text += ev.Text
		m.Streaming = true
		a.streaming = true
		// 纯文本回合也必须落盘：过去只有工具/终态才写，掉电会丢整轮回答。
		if time.Since(a.lastDisk) >= 400*time.Millisecond {
			a.lastDisk = time.Now()
			a.saveConv()
		}
	case "reasoning":
		if m == nil {
			m = a.newAssistantMsg()
		}
		m.Reasoning += ev.Text
		m.Streaming = true
		a.streaming = true
	case "tool_start":
		if m == nil {
			m = a.newAssistantMsg()
		}
		m.Tools = append(m.Tools, &ToolCard{
			ID: ev.ID, Name: ev.Name, Args: ev.Args, Started: time.Now(),
		})
		a.streaming = true
	case "tool_end":
		if ev.Name != "" {
			d := ev.Dur
			if d <= 0 {
				d = 0
			}
			a.observeTool(ev.Name, d, !ev.Err)
		}
		if m != nil {
			for i := len(m.Tools) - 1; i >= 0; i-- {
				if m.Tools[i].ID == ev.ID || (ev.ID == "" && !m.Tools[i].Done) {
					m.Tools[i].Output = ev.Text
					m.Tools[i].Done = true
					m.Tools[i].Err = ev.Err
					// 用引擎测出的真实耗时：事件跨 goroutine 送到界面会有延迟，
					// 界面自己算会把渲染排队的时间也算进去。
					if ev.Dur > 0 {
						m.Tools[i].Elapsed = ev.Dur
					} else {
						m.Tools[i].Elapsed = time.Since(m.Tools[i].Started)
					}
					break
				}
			}
		}
		// 工具级进度：每个工具完成就更新日志，并把消息增量写盘（1 秒节流）。
		// 掉电最多丢最后一个工具的结果，而不是整轮对话。
		a.journalProgress(ev.Name)
		if time.Since(a.lastDisk) >= time.Second {
			a.lastDisk = time.Now()
			a.saveConv()
		}
	case "turn_end":
		a.streaming = false
		a.retryBackup = nil
		a.observeTurn(true)
		// 最后一条消息从“流式”切成“完成”，高度/操作条都会变；强制整帧
		// 重绘，避免脏区算法在收尾这一帧留下空白带。
		a.forceFull = true
		if m != nil {
			m.Streaming = false
			fmt.Sscanf(ev.Text, "%d", &m.Tokens)
			if conv != nil {
				conv.Tok += m.Tokens
			}
		}
		a.curAsst = -1
		a.autoScroll = true
		a.saveConv()
		// 轮次正常结束：删掉恢复日志，下次启动不会再提示。
		a.journalFinish()
		// 人去干别的了才弹气泡；窗口就在眼前时气泡只会挡路。
		if a.w != nil && win32.ForegroundWindow() != a.w.Hwnd() {
			tail := ""
			if m != nil {
				tail = oneLine(lastTextLine(m.Text))
			}
			a.notify("回答已完成", tail, false)
		}
	case "error":
		a.forceFull = true
		if m != nil {
			m.Err = ev.Text
			m.Streaming = false
		} else {
			a.msgs = append(a.msgs, Message{Role: "assistant", Err: ev.Text, At: time.Now()})
		}
		a.streaming = false
		a.observeTurn(false)
		a.curAsst = -1
		// 重试二次失败时把旧回复放回去，不让用户丢失上一条答案。
		if a.retryBackup != nil {
			a.msgs = append(a.msgs, *a.retryBackup)
			a.retryBackup = nil
		}
		a.saveConv()
		// 出错也是明确的结束状态，保留日志只会让用户以为还能续做。
		a.journalFinish()
		a.toast(ev.Text, true)
		// 失败自动留证据：诊断包里有脱敏配置、环境与最近日志，
		// 用户报问题时直接给这一个文件，不用再问"你什么系统什么模型"。
		if p := a.autoDiag(ev.Text); p != "" {
			a.notify("任务失败，已生成诊断包", oneLine(ev.Text), true)
		} else {
			a.notify("任务失败", oneLine(ev.Text), true)
		}
	case "status":
		a.statusMsg = ev.Text
		// 引擎侧停止只发 status，这里兜住终态，避免界面卡在"生成中"。
		if ev.Text == "已停止" {
			a.streaming = false
			a.forceFull = true
			a.curAsst = -1
			if m != nil {
				m.Streaming = false
			}
			a.journalFinish()
			a.saveConv()
		}
	case "plan":
		// 审批闸门已由引擎的 PlanApprove 回调接管：事件到达时弹窗通常已经（或即将）挂起。
		// 这里只更新状态栏，绝不再覆写 a.input——那会把用户正在打的字吃掉。
		a.statusMsg = "方案待确认：在弹窗中选择批准、拒绝或修改"
	case "notice":
		a.forceFull = true
		a.msgs = append(a.msgs, Message{Role: "assistant", Text: ev.Text, At: time.Now()})
		a.autoScroll = true
		a.saveConv()
	case "budget":
		a.forceFull = true
		if m != nil {
			m.Streaming = false
		}
		a.streaming = false
		a.curAsst = -1
		a.msgs = append(a.msgs, Message{Role: "assistant", Err: ev.Text, At: time.Now()})
		a.autoScroll = true
		a.saveConv()
		a.journalFinish()
		a.toast("已触发预算护栏，停止继续调用模型", true)
	}
	a.ver++
	if a.autoScroll {
		a.scrollToBottom()
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

// DoubleClick 在主输入区双击选中一个词。
func (a *App) DoubleClick(x, y int32) {
	a.beat()
	if a.ui == nil {
		return
	}
	if a.page != PageChat || a.ui.HitAt(x, y) != "input" {
		return
	}
	p := a.inputHitRune(x, y)
	isWord := func(r rune) bool {
		return r != ' ' && r != '\n' && r != '\t' && r != '/' && r != '.' && r != ',' && r != ':' && r != ';' && r != '\''
	}
	lo, hi := p, p
	for lo > 0 && isWord(a.input[lo-1]) {
		lo--
	}
	for hi < len(a.input) && isWord(a.input[hi]) {
		hi++
	}
	a.inputSel, a.inputCur = lo, hi
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
			// 恢复历史（只恢复 user/assistant 文本，工具细节不重放）
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
	r := ui.R((a.winW-rw)/2, a.winH-140, rw, 34)
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
