package app

// Option A：kit 组件框架的新页面（会话中心 / 工作区总览）。
// 桌面上：rail + 顶栏 + 内容，全部单独实现：
//   rail / 顶栏与 rail 分栏沿用 classic 逻辑（同记令牌），
//   内容区（含列表）完全用 kit 声明式组件树画。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"win7harness/internal/agent"
	"win7harness/internal/codeidx"
	"win7harness/internal/core"
	"win7harness/internal/ui"
	"win7harness/internal/ui/kit"
)

// kitCtx 拿到（共享）的 kit Ctx，lazy 构建一次
func (a *App) kit() *kit.Ctx {
	if a.kitui == nil {
		ui.SetTokens(a.th.Tokens())
		a.kitui = kit.New(kit.NewCanvasAdapter(a.ui.c))
	}
	return a.kitui
}

// ---------- 会话中心 ----------

type sessItem struct {
	ID      string
	Title   string
	At      time.Time
	Msgs    int
	Preview string
	Size    string
}

const sessRowHitPrefix = "s:row:"

// sessIndexState 是会话中心的索引快照。core.SessionIndex 要读取并解析
// 会话文件头，过去在第一次绘制时同步执行，大目录/大文件会让页面"点开就卡住"。
// 现在改为后台线程 + 快照，页面先显示"读取中"。
type sessIndexState struct {
	mu    sync.Mutex
	items []sessItem
	at    time.Time
	busy  bool
	err   string
}

func newSessIndexState() *sessIndexState { return &sessIndexState{} }

// sessRow：整行可点击卡片（悬停混合、按压反馈）
type sessRow struct {
	ID       string
	Item     sessItem
	Pinned   bool
	OnClick  func()
	OnPin    func()
	OnDelete func()
	r        kit.Rect
}

func (e *sessRow) Measure(c *kit.Ctx, cons kit.Constraint) kit.Size { // nolint:nilerr
	return kit.Size{W: cons.MaxW, H: 60}
}

func (e *sessRow) Arrange(c *kit.Ctx, r kit.Rect) { e.r = r }

func (e *sessRow) Paint(c *kit.Ctx, g kit.Painter) {
	r := e.r
	tk := ui.CurrentTokens()
	id := e.ID
	// 先注册整行命中（动作按钮后注册，逆序命中时优先）。
	c.RegisterHit(id, r, e.OnClick)
	// 卡片底色：常态 + hover + 按压混色
	fill := tk.BGSurfaceRaise
	if c.IsHovered(r) {
		fill = tk.BGHover
	}
	if c.IsPressed(id) {
		fill = tk.BGActive
	}
	g.Round(r, 8, fill)
	g.RoundStroke(r, 8, tk.BorderHairline)

	// 标记块：消息数
	badge := ui.R(r.X+10, r.Y+13, 34, 34)
	g.Round(badge, 8, tk.AccentSubtle)
	count := e.Item.Msgs
	label := fmt.Sprintf("%d", count)
	if count > 99 {
		label = "99+"
	}
	if count < 0 {
		count = 0
		label = "0"
	}
	g.TextRectFace(badge, label, ui.FsSm, 700, tk.AccentBase, 0x0021, ui.FaceUI)
	tx := r.X + 54

	// 标题与预览各自截断
	title := e.Item.Title
	if e.Pinned {
		title = "★ " + title
	}
	g.TextFace(tx, r.Y+8, kit.TextTrunc(c, title, ui.FsSm, 700, r.Right()-tx-130, ui.FaceUI), ui.FsSm, 700, tk.TextPrimary, ui.FaceUI)
	g.TextFace(tx, r.Y+26, kit.TextTrunc(c, e.Item.Preview, ui.FsXs, 400, r.Right()-tx-130, ui.FaceUI), ui.FsXs, 400, tk.TextTertiary, ui.FaceUI)

	// 右侧：时间 + 体积；悬停时换成置顶/删除操作，避免常驻按钮噪声。
	rg := r.Right() - 116
	at := e.Item.At.Format("01-02 15:04")
	if c.IsHovered(r) {
		if e.OnPin != nil {
			pinID := id + ":pin"
			pr := ui.R(r.Right()-144, r.Y+18, 52, 24)
			g.Round(pr, 6, tk.BGSurface)
			g.RoundStroke(pr, 6, tk.BorderHairline)
			g.TextRectFace(pr, "置顶", ui.FsXs, 400, tk.TextSecondary, 0x0021, ui.FaceUI)
			c.RegisterHit(pinID, pr, e.OnPin)
		}
		if e.OnDelete != nil {
			delID := id + ":del"
			dr := ui.R(r.Right()-88, r.Y+18, 52, 24)
			g.Round(dr, 6, tk.BGSurface)
			g.RoundStroke(dr, 6, tk.BorderHairline)
			g.TextRectFace(dr, "删除", ui.FsXs, 400, tk.StateDanger, 0x0021, ui.FaceUI)
			c.RegisterHit(delID, dr, e.OnDelete)
		}
	} else {
		g.TextFace(rg, r.Y+8, at, ui.FsXs, 400, tk.TextTertiary, ui.FaceUI)
		g.TextFace(rg, r.Y+26, e.Item.Size, ui.FsXs, 400, tk.TextTertiary, ui.FaceUI)
	}
}

func (a *App) paintSessionsPage(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH

	c.FillRect(ui.R(0, 0, W, H), th.Bg)
	a.paintRail(u, H)

	// 顶栏
	c.FillRect(ui.R(a.railW, 0, W-a.railW, 56), ui.Blend(th.Bg, th.Surface, 0.6))
	c.HLine(a.railW, 55, W-a.railW, th.BorderSoft)
	DrawLogo(c, a.railW+24, 16, 24, th.Accent, th.Bg)
	c.TextFace(a.railW+56, 18, "会话中心", ui.FsLg, 700, th.Text, ui.FaceUI)
	// 快捷回车：esc 或返回按钮回到对话
	if u.Button("sessions.back", "← 返回对话", ui.R(W-140, 14, 116, 30), BtnGhost) {
		a.page = PageChat
	}

	// ---------- kit 内容区 ----------
	ui.SetTokens(a.th.Tokens())
	ktx := a.kit()
	ktx.Painter = kit.NewCanvasAdapter(c)
	ktx.BeginFrame()

	items := a.ensureSessions()
	busy := a.sessionIndexBusy()
	q := strings.ToLower(strings.TrimSpace(kit.InputText(ktx, "s:q")))

	var rows []kit.Element
	for _, it := range items {
		if q != "" && !strings.Contains(strings.ToLower(it.Title+" "+it.Preview), q) {
			continue
		}
		it := it
		pinned := false
		if i := a.convIndexByID(it.ID); i >= 0 {
			pinned = a.convs[i].Pinned
		}
		rows = append(rows, &sessRow{
			ID:     sessRowHitPrefix + it.ID,
			Item:   it,
			Pinned: pinned,
			OnClick: func() {
				a.activateSession(it.ID)
				a.sessDirty = true
			},
			OnPin: func() {
				if i := a.convIndexByID(it.ID); i >= 0 {
					a.togglePin(i)
					a.sessDirty = true
					a.Redraw()
				}
			},
			OnDelete: func() {
				if i := a.convIndexByID(it.ID); i >= 0 {
					a.requestDeleteConv(i)
				} else {
					a.toast("会话尚未载入，请先打开一次再删除", true)
				}
			},
		})
	}
	listCol := kit.Column(8, rows...)
	if len(rows) == 0 {
		title, hint := "没有匹配的会话", "试试换个关键词；也可以去别的会话里继续对话。"
		if busy && len(items) == 0 {
			title, hint = "正在读取会话…", "会话索引在后台线程解析，目录里会话很多时也不会卡住界面。"
		}
		listCol = kit.Column(8,
			kit.Spacer(),
			kit.Label(title, ui.FsMd, 700, ui.CurrentTokens().TextSecondary),
			kit.Label(hint, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
		)
	}
	countLabel := fmt.Sprintf("全部保存的会话 · %d 条", len(items))
	if busy {
		countLabel = fmt.Sprintf("全部保存的会话 · %d 条（刷新中）", len(items))
	}
	content := kit.Column(10,
		kit.Row(8,
			kit.Label(countLabel, ui.FsSm, 700, ui.CurrentTokens().TextSecondary),
			kit.Spacer(),
			kit.Button("s:new", "+ 新建", kit.ButtonPrimary, func() { a.page = PageChat; a.newConv() }),
			kit.Button("s:refresh", "刷新", kit.ButtonSecondary, func() { a.sessDirty = true }),
		),
		kit.Input("s:q", "搜索标题或内容…", 60, nil),
		kit.Scroll("s:main", H-130, listCol),
	)
	area := kit.R(a.railW+16, 66, W-a.railW-32, H-66-8)
	content.Arrange(ktx, area)
	content.Paint(ktx, ktx.Painter)
}

func (a *App) ensureSessions() []sessItem {
	s := a.sessIdx
	if s == nil {
		return nil
	}
	s.mu.Lock()
	need := a.sessDirty || s.at.IsZero() || time.Since(s.at) > 15*time.Second
	out := append([]sessItem(nil), s.items...)
	s.mu.Unlock()
	if need {
		a.sessDirty = false
		a.triggerSessionIndex()
	}
	return out
}

// triggerSessionIndex 启动一次后台会话索引刷新；已有一轮在跑时直接复用。
func (a *App) triggerSessionIndex() {
	s := a.sessIdx
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return
	}
	s.busy = true
	s.mu.Unlock()
	core.SafeGo("app/pages_kit.go:sessionIndex", func() { a.refreshSessionIndex() })
}

func (a *App) refreshSessionIndex() {
	s := a.sessIdx
	if s == nil {
		return
	}
	list, err := core.SessionIndex(core.SessionsDir(), 2000)
	var items []sessItem
	if err == nil {
		items = make([]sessItem, 0, len(list))
		for _, b := range list {
			items = append(items, sessItem{
				ID: b.ID, Title: b.Title, At: b.At, Msgs: b.Msgs, Preview: b.Preview, Size: core.HumanSize(b.Size),
			})
		}
	}
	s.mu.Lock()
	s.items = items
	s.err = ""
	if err != nil {
		s.err = err.Error()
	}
	s.at = time.Now()
	s.busy = false
	s.mu.Unlock()
	a.Redraw()
}

func (a *App) sessionIndexBusy() bool {
	s := a.sessIdx
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy
}

// convIndexByID 按稳定 ID 找会话下标；找不到返回 -1。
func (a *App) convIndexByID(id string) int {
	for i := range a.convs {
		if a.convs[i].ID == id {
			return i
		}
	}
	return -1
}

func (a *App) activateSession(id string) {
	if i := a.convIndexByID(id); i >= 0 && a.convs[i].Loaded {
		a.active = i
		a.page = PageChat
		a.loadConv()
		return
	}
	a.loadConvAsync(id, func(*Conv) {
		if i := a.convIndexByID(id); i >= 0 {
			a.active = i
			a.page = PageChat
			a.loadConv()
		}
	})
}

// loadConvAsync 后台读取一个会话；then 会在 UI 线程、正文就绪后执行。
func (a *App) loadConvAsync(id string, then func(*Conv)) {
	if a.convLoading == id {
		return
	}
	a.convLoading = id
	a.toast("正在载入会话…", false)
	core.SafeGo("app/pages_kit.go:loadSession", func() {
		cv, err := readConvByID(id)
		select {
		case a.convLoadCh <- convLoadResult{id: id, cv: cv, err: err, then: then}:
		default:
			// 通道满说明用户在连点；丢结果不会丢会话，下次再点即可。
		}
	})
}

type convLoadResult struct {
	id   string
	cv   *Conv
	err  error
	then func(*Conv) // 载入完成后的 UI 线程动作（置顶/改名等）
}

// drainConvLoads 在 UI 线程消费后台打开会话的结果。
func (a *App) drainConvLoads() {
	for {
		select {
		case res := <-a.convLoadCh:
			if a.convLoading == res.id {
				a.convLoading = ""
			}
			if res.err != nil || res.cv == nil {
				msg := "打开会话失败"
				if res.err != nil {
					msg += "：" + res.err.Error()
				}
				a.toast(msg, true)
				continue
			}
			cv := res.cv
			cv.Loaded = true
			cv.MsgN = len(cv.Msgs)
			cv.Preview = convLastPreview(cv)
			idx := a.convIndexByID(cv.ID)
			if idx >= 0 {
				a.convs[idx] = cv
			} else {
				a.convs = append(a.convs, cv)
			}
			core.Logf("session-loaded id=%s msgs=%d", cv.ID, len(cv.Msgs))
			if res.then != nil {
				res.then(cv)
			}
			a.ver++
		default:
			return
		}
	}
}

// hydrateConvs 后台补全列表里的空壳会话。只补前 N 条，老会话等点开再读，
// 避免超大历史库在启动后立刻把磁盘读满。
func (a *App) hydrateConvs(ids []string) {
	for _, id := range ids {
		if id == "" {
			continue
		}
		cv, err := readConvByID(id)
		if err != nil || cv == nil {
			continue
		}
		cv.Loaded = true
		cv.MsgN = len(cv.Msgs)
		cv.Preview = convLastPreview(cv)
		select {
		case a.hydrateCh <- cv:
		default:
			// 队列满：跳过这一条，搜索/点开时仍可单独加载。
		}
		time.Sleep(35 * time.Millisecond)
	}
}

// drainHydration 把后台补全的正文换回列表，不切换当前页面。
func (a *App) drainHydration() {
	for {
		select {
		case cv := <-a.hydrateCh:
			idx := a.convIndexByID(cv.ID)
			if idx >= 0 && !a.convs[idx].Loaded {
				a.convs[idx] = cv
			}
			a.ver++
		default:
			return
		}
	}
}

func convLastPreview(cv *Conv) string {
	for i := len(cv.Msgs) - 1; i >= 0; i-- {
		t := strings.TrimSpace(cv.Msgs[i].Text)
		if t == "" {
			continue
		}
		return clipSnippet(t, 0, 24)
	}
	return ""
}

// ---------- 工作区总览 ----------

type wsEntry struct {
	path  string
	rel   string
	bytes int64
}

type wsStats struct {
	count    int
	bytes    int64
	bigCount int
	big      []wsEntry
}

// wsState 是工作区扫描的后台状态。filepath.Walk、git 分支、git 变更
// 都是慢操作（大仓库可达数秒、每次起子进程几十毫秒），绝不能放在 Paint 里。
// 页面只读快照；第一次进入显示"扫描中"，扫描完成后自动刷新。
type wsState struct {
	mu      sync.Mutex
	dir     string
	stats   wsStats
	branch  string
	changes int
	at      time.Time
	busy    bool
	err     string
	gen     int
}

func newWSState() *wsState { return &wsState{} }

func (a *App) invalidateWorkspace() {
	s := a.ws
	if s == nil {
		return
	}
	s.mu.Lock()
	s.dir = ""
	s.at = time.Time{}
	s.gen++
	s.mu.Unlock()
}

func (a *App) ensureWorkspaceScan(dir string) wsStats {
	s := a.ws
	if s == nil || dir == "" {
		return wsStats{}
	}
	s.mu.Lock()
	if s.dir == dir && !s.at.IsZero() {
		out := s.stats
		s.mu.Unlock()
		return out
	}
	if s.busy && s.dir == dir {
		out := s.stats
		s.mu.Unlock()
		return out
	}
	s.dir = dir
	s.busy = true
	s.at = time.Time{}
	s.gen++
	gen := s.gen
	s.mu.Unlock()
	core.SafeGo("app/pages_kit.go:scanWorkspace", func() {
		a.scanWorkspace(dir, gen)
	})
	return wsStats{}
}

func (a *App) scanWorkspace(dir string, gen int) {
	ws := wsStats{}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == ".cache" {
				return filepath.SkipDir
			}
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return nil
		}
		if ws.count >= wsScanLimit {
			return filepath.SkipAll
		}
		ws.count++
		ws.bytes += st.Size()
		if st.Size() > wsScanBigBytes {
			ws.bigCount++
			rel, err := filepath.Rel(dir, path)
			if err == nil {
				path = filepath.ToSlash(rel)
				if len(path) > 72 {
					path = "…" + path[len(path)-71:]
				}
				ws.big = append(ws.big, wsEntry{path: path, rel: path, bytes: st.Size()})
			}
		}
		return nil
	})
	sort.Slice(ws.big, func(i, j int) bool { return ws.big[i].bytes > ws.big[j].bytes })
	if len(ws.big) > 10 {
		ws.big = ws.big[:10]
	}
	branch := ""
	if b, err := core.GitRun(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		x := strings.TrimSpace(b)
		if x != "" && x != "HEAD" {
			branch = x
		}
	}
	changes := len(core.GitChanges(dir))

	s := a.ws
	if s == nil {
		return
	}
	s.mu.Lock()
	// 扫描期间用户可能切到别的目录或点了"重新扫描"：过期结果直接丢弃。
	if s.dir == dir && s.gen == gen {
		s.stats = ws
		s.branch = branch
		s.changes = changes
		s.at = time.Now()
		s.busy = false
	}
	s.mu.Unlock()
	a.Redraw()
}

func (a *App) workspaceSnapshot(dir string) (wsStats, string, int, time.Time, bool) {
	s := a.ws
	if s == nil {
		return wsStats{}, "", 0, time.Time{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats, s.branch, s.changes, s.at, s.busy && s.dir == dir
}

// changedFile 是"本会话 agent 动过的文件"一行的数据。
type changedFile struct {
	Path    string
	Abs     string
	Op      string
	At      time.Time
	Err     bool
	Running bool
}

// sessionChangedFiles 从工具卡里提取本会话的写文件/改文件/删文件记录。
// 这是工作区总览最该先回答的问题：这一轮到底动了哪些文件。
func (a *App) sessionChangedFiles(limit int) []changedFile {
	wd := a.sessionWorkDir()
	if wd == "" {
		return nil
	}
	if a.changedCache != nil && a.changedVer == a.ver && a.changedWD == wd {
		if len(a.changedCache) <= limit {
			return a.changedCache
		}
		return a.changedCache[:limit]
	}
	seen := map[string]bool{}
	out := make([]changedFile, 0, 8)
	for mi := len(a.msgs) - 1; mi >= 0; mi-- {
		m := &a.msgs[mi]
		for ti := len(m.Tools) - 1; ti >= 0; ti-- {
			t := m.Tools[ti]
			if t == nil {
				continue
			}
			op := ""
			switch t.Name {
			case "write_file":
				op = "写入"
			case "edit_file":
				op = "修改"
			case "delete_path":
				op = "删除"
			case "apply_patch":
				op = "补丁"
			default:
				continue
			}
			raw := toolPathArg(t)
			if raw == "" {
				continue
			}
			abs := core.ResolvePath(wd, filepath.FromSlash(raw))
			if abs == "" || seen[abs] || core.IsForbiddenPath(abs) || core.EscapesWorkDir(wd, abs) {
				continue
			}
			seen[abs] = true
			out = append(out, changedFile{
				Path: raw, Abs: abs, Op: op, At: t.Started,
				Err: t.Err, Running: !t.Done,
			})
			if len(out) >= limit {
				a.changedCache = out
				a.changedVer = a.ver
				a.changedWD = wd
				return out
			}
		}
	}
	a.changedCache = out
	a.changedVer = a.ver
	a.changedWD = wd
	return out
}

// toolPathArg 尽量从工具 JSON 参数里取出目标路径；apply_patch 从补丁头提取。
func toolPathArg(t *ToolCard) string {
	var m map[string]interface{}
	if json.Unmarshal([]byte(t.Args), &m) != nil {
		return ""
	}
	for _, k := range []string{"path", "file", "target"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	if t.Name == "apply_patch" {
		if patch, ok := m["patch"].(string); ok {
			for _, line := range strings.Split(patch, "\n") {
				for _, p := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
					if strings.HasPrefix(line, p) {
						return strings.TrimSpace(strings.TrimPrefix(line, p))
					}
				}
			}
		}
	}
	return ""
}

func (a *App) paintWorkspacePage(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH

	c.FillRect(ui.R(0, 0, W, H), th.Bg)
	a.paintRail(u, H)

	// 顶栏
	c.FillRect(ui.R(a.railW, 0, W-a.railW, 56), ui.Blend(th.Bg, th.Surface, 0.6))
	c.HLine(a.railW, 55, W-a.railW, th.BorderSoft)
	DrawLogo(c, a.railW+24, 16, 24, th.Accent, th.Bg)
	c.TextFace(a.railW+56, 18, "工作区总览", ui.FsLg, 700, th.Text, ui.FaceUI)
	if u.Button("ws.back", "← 返回对话", ui.R(W-140, 14, 116, 30), BtnGhost) {
		a.page = PageChat
	}

	// kit 内容区
	ui.SetTokens(a.th.Tokens())
	ktx := a.kit()
	ktx.Painter = kit.NewCanvasAdapter(c)
	ktx.BeginFrame()

	wd := strings.TrimSpace(a.convWorkDir())
	if wd == "" {
		c.TextFace(a.railW+32, 110, "尚未设置工作目录", ui.FsMd, 700, th.TextDim, ui.FaceUI)
		c.TextFace(a.railW+32, 140, "去设置页选一个项目目录，工作区总览才有数可算。", ui.FsSm, 400, th.TextMute, ui.FaceUI)
		if u.Button("ws.toset", "去设置", ui.R(a.railW+32, 170, 96, 30), BtnPrimary) {
			a.page = PageSettings
		}
		return
	}
	// 页面只读后台扫描快照；首次进入时先显示"扫描中"，不阻塞 Paint。
	a.ensureWorkspaceScan(wd)
	st, branch, changes, scannedAt, scanning := a.workspaceSnapshot(wd)
	ixFiles, ixSyms := 0, 0
	if ix := codeidx.Cached(); ix != nil {
		if f, s := ix.Stats(); f > 0 {
			ixFiles, ixSyms = f, s
		}
	}

	status := "扫描于 " + scannedAt.Format("15:04:05")
	if scanning || scannedAt.IsZero() {
		status = "正在扫描目录（后台进行，界面不阻塞）…"
	}
	stats := []struct{ label, value string }{
		{"工作目录", wd},
		{"文件数", fmt.Sprintf("%d", st.count)},
		{"总大小", core.HumanSize(st.bytes)},
		{"大于 1MB", fmt.Sprintf("%d", st.bigCount)},
		{"当前分支", branch},
		{"待提交改动", fmt.Sprintf("%d", changes)},
		{"索引文件", fmt.Sprintf("%d", ixFiles)},
		{"索引符号", fmt.Sprintf("%d", ixSyms)},
	}
	statCards := make([]kit.Element, 0, len(stats))
	for _, s := range stats {
		v := s.value
		if v == "" {
			v = "—"
		}
		statCards = append(statCards, kit.Column(2,
			kit.Label(s.label, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
			kit.Label(v, ui.FsMd, 700, ui.CurrentTokens().TextPrimary),
		))
	}
	grid := kit.Row(12, statCards...)

	bigRows := []kit.Element{kit.Column(2,
		kit.Label("Top 大文件", ui.FsSm, 700, ui.CurrentTokens().TextSecondary),
	)}
	for i, f := range st.big {
		bigRows = append(bigRows, kit.Row(8,
			kit.Label(fmt.Sprintf("%d", i+1), ui.FsXs, 700, ui.CurrentTokens().TextTertiary),
			kit.Label(f.rel, ui.FsXs, 400, ui.CurrentTokens().TextSecondary),
			kit.Spacer(),
			kit.Label(core.HumanSize(f.bytes), ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
		))
	}
	if len(st.big) == 0 {
		bigRows = append(bigRows, kit.Label("没有超过 1MB 的文件。", ui.FsXs, 400, ui.CurrentTokens().TextTertiary))
	}

	changeRows := []kit.Element{kit.Column(2,
		kit.Label("本会话改动", ui.FsSm, 700, ui.CurrentTokens().TextSecondary),
		kit.Label("来自 write_file / edit_file / apply_patch / delete_path 的实际调用记录", ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
	)}
	changesList := a.sessionChangedFiles(12)
	if len(changesList) == 0 {
		changeRows = append(changeRows, kit.Label("本会话还没有文件改动。", ui.FsXs, 400, ui.CurrentTokens().TextTertiary))
	}
	for _, f := range changesList {
		f := f
		exists, isDir := a.pathStateCached(f.Abs)
		state := "存在"
		if f.Running {
			state = "进行中"
		} else if f.Err {
			state = "失败"
		} else if !exists || isDir {
			state = "已删除"
		}
		sub := fmt.Sprintf("%s · %s · %s", f.Op, state, f.At.Format("15:04:05"))
		changeRows = append(changeRows, kit.Row(8,
			kit.Column(1,
				kit.Label(kit.TextTrunc(ktx, f.Path, ui.FsXs, 400, W-a.railW-200, ui.FaceMono), ui.FsXs, 400, ui.CurrentTokens().TextPrimary),
				kit.Label(sub, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
			),
			kit.Spacer(),
			kit.Button("ws.open:"+f.Abs, "打开", kit.ButtonGhost, func() {
				a.openPathAtLine(f.Abs, 0)
			}),
		))
	}

	content := kit.Column(12,
		kit.Row(8,
			kit.Label(status, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
			kit.Spacer(),
			kit.Button("w:rescan", "重新扫描", kit.ButtonGhost, func() { a.invalidateWorkspace() }),
			kit.Button("w:open", "打开目录", kit.ButtonSecondary, func() { revealPath(wd, false) }),
			kit.Button("w:reindex", "重建索引", kit.ButtonPrimary, func() { a.queueIndexRebuild(wd) }),
		),
		kit.Card(10, grid),
		kit.Card(10, kit.Column(6, changeRows...)),
		kit.Card(10, kit.Column(6, bigRows...)),
	)
	area := kit.R(a.railW+16, 66, W-a.railW-32, H-66-8)
	scroll := kit.Scroll("w:main", 0, content)
	scroll.Arrange(ktx, area)
	scroll.Paint(ktx, ktx.Painter)
}

const wsScanLimit = 40000
const wsScanBigBytes = int64(1 << 20)

// queueIndexRebuild 后台重建代码索引，完成后事件通道通知。
func (a *App) queueIndexRebuild(wd string) {
	a.toast("正在后台重建代码索引…", false)
	core.SafeGo("app/pages_kit.go:reindex", func() {
		codeidx.Refresh(wd)
	})
}

// ---------- 成本与预算中心（kit） ----------

func (a *App) paintCostsPage(c *ui.Canvas) {
	th := a.th
	u := a.ui
	W, H := a.winW, a.winH

	c.FillRect(ui.R(0, 0, W, H), th.Bg)
	a.paintRail(u, H)

	// 顶栏
	c.FillRect(ui.R(a.railW, 0, W-a.railW, 56), ui.Blend(th.Bg, th.Surface, 0.6))
	c.HLine(a.railW, 55, W-a.railW, th.BorderSoft)
	DrawLogo(c, a.railW+24, 16, 24, th.Accent, th.Bg)
	c.TextFace(a.railW+56, 18, "成本与预算中心", ui.FsLg, 700, th.Text, ui.FaceUI)
	if u.Button("cost.back", "← 返回对话", ui.R(W-140, 14, 116, 30), BtnGhost) {
		a.page = PageChat
	}

	// kit 内容区
	ui.SetTokens(a.th.Tokens())
	ktx := a.kit()
	ktx.Painter = kit.NewCanvasAdapter(c)
	ktx.BeginFrame()

	snap := agent.CostSnapshot{}
	if a.eng != nil {
		snap = a.eng.CostSnapshot()
	}

	header := kit.Row(8,
		kit.Label("当前会话的使用成本（本地价目表估算）", ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
		kit.Spacer(),
		kit.Button("cp:settings", "去设置改预算", kit.ButtonSecondary, func() { a.page = PageSettings }),
	)

	hitPct := int(snap.HitRate * 100)
	costBig := kit.Column(4,
		kit.Label(core.Money(snap.Cost), ui.FsXl, 700, ui.CurrentTokens().TextPrimary),
		kit.Label(fmt.Sprintf("%d 轮 · 输入 %s · 输出 %s · 缓存命中 %d%%",
			snap.Turns, humanTokens(snap.TokIn), humanTokens(snap.TokOut), hitPct), ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
	)

	parts := []struct {
		label string
		rate  string
		turns int
		cost  float64
	}{
		{"主对话", snap.Model, snap.Turns, snap.Cost},
		{"快速模型", snap.FastModel, snap.FastTurns, snap.FastCost},
		{"子代理", "task 派发", snap.SubTurns, snap.SubCost},
	}
	partCards := make([]kit.Element, 0, len(parts))
	for _, p := range parts {
		note := p.rate
		if note == "" {
			note = "未设置"
		}
		partCards = append(partCards, kit.Expanded(kit.Card(12, kit.Column(4,
			kit.Label(p.label, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
			kit.Label(core.Money(p.cost), ui.FsMd, 700, ui.CurrentTokens().TextPrimary),
			kit.Label(fmt.Sprintf("%d 次 · %s", p.turns, note), ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
		))))
	}
	grid := kit.Row(12, partCards...)

	var budgetRows []kit.Element
	cfg := a.cfg
	if cfg != nil {
		if cfg.MaxCost > 0 {
			budgetRows = append(budgetRows, budgetCard("花费上限", snap.Cost, cfg.MaxCost,
				core.Money(snap.Cost)+" / "+core.Money(cfg.MaxCost)))
		}
		if cfg.BudgetTokens > 0 {
			used := float64(snap.TokIn + snap.TokOut)
			budgetRows = append(budgetRows, budgetCard("Token 上限", used, float64(cfg.BudgetTokens),
				humanTokens(snap.TokIn+snap.TokOut)+" / "+humanTokens(cfg.BudgetTokens)))
		}
	}
	if len(budgetRows) == 0 {
		budgetRows = append(budgetRows, kit.Card(12, kit.Column(4,
			kit.Label("尚未设置预算上限", ui.FsSm, 700, ui.CurrentTokens().TextSecondary),
			kit.Label("给大任务兜底：在设置页「预算与限制」设一个花费上限。", ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
		)))
	}

	recap := kit.Column(10, budgetRows...)

	content := kit.Column(14,
		header,
		costBig,
		grid,
		recap,
		kit.Label("缓存命中可按缓存价计费；子代理快速路由与主对话分桶核算。估算仅供参考，以服务商账单为准。", ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
	)
	area := kit.R(a.railW+16, 66, W-a.railW-32, H-66-8)
	scroll := kit.Scroll("cost:main", 0, content)
	scroll.Arrange(ktx, area)
	scroll.Paint(ktx, ktx.Painter)
}

// budgetCard 生成“已用 / 上限”卡片（不新增 kit 原语，直接复用 Label/Card）。
func budgetCard(label string, used, limit float64, text string) kit.Element {
	pct := 0.0
	if limit > 0 {
		pct = used / limit
	}
	return kit.Card(12, kit.Column(4,
		kit.Label(label, ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
		kit.Label(text, ui.FsMd, 700, ui.CurrentTokens().TextPrimary),
		kit.Label(fmt.Sprintf("已用 %.1f%%", pct*100), ui.FsXs, 400, ui.CurrentTokens().TextTertiary),
	))
}
