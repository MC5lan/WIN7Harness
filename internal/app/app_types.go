//go:build windows

package app

import (
	"sync"
	"sync/atomic"
	"time"
	"win7harness/internal/agent"
	"win7harness/internal/core"
	"win7harness/internal/llm"
	"win7harness/internal/ui"
	"win7harness/internal/ui/kit"
)

// 本文件由 app.go 按职责拆分（T09），仅移动代码，未改行为。
// 职责：Page / Conv / toast / App 等核心类型定义。

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
	// EngineMsgs 是恢复给 Agent 的结构化轨迹（含工具调用/结果）。
	// 只保存最近一段并去掉图片载荷；旧会话没有该字段时回落到 Msgs 文本回灌。
	EngineMsgs []llm.Message `json:"engine_msgs,omitempty"`
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
	// lastCfgPublish 控制配置快照的发布频率：UI 线程每秒把 a.cfg 的
	// 深拷贝发布到 core.current，工具 goroutine 只读快照，不再和设置页并发读写。
	lastCfgPublish time.Time
	// uiMu 保护 uiQueue：后台 goroutine 可以 queueUI，Timer 在 UI 线程消费。
	uiMu sync.Mutex
	// bootAt/healthMarked 用于更新后的启动健康标记：主循环稳定跑 3 秒后
	// 写 update-healthy.txt，替换脚本据此判断新版本是否真的起来了。
	bootAt       time.Time
	healthMarked bool
	eng          *agent.Engine
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
	// settingsBaseline 是进入设置页时的字段指纹；用于显示“未保存修改”，
	// 只存哈希，不把 API Key 等原文留在额外字符串里。
	settingsBaseline uint64
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
	// trayFails 记录托盘挂载失败次数：Explorer 重启/桌面未就绪时首次
	// Shell_NotifyIconW 可能失败，后面几帧再试，避免“右下角永远看不到图标”。
	trayFails int
	lastDiag  time.Time
	qr        *core.QRCode
	qrURL     string
	qrUntil   time.Time

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
	presetIdx int
	// onbStep 是引导页当前步骤：0 品牌欢迎页，1 连接模型（调试）。
	onbStep int
	// onboardScrl/onboardH 是连接模型页内容区滚动：低分辨率 + 高 DPI 下
	// 表单比卡片高，必须能滚动，否则字段和“开始使用”会重叠。
	onboardScrl float32
	onboardH    int32
	testMsg     string
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
