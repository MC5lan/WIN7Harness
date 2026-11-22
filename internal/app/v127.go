package app

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/remote"
)

// v1.27 运行时：手机指挥台、定时任务、目录守护、更新检查。
//
// 这些能力的共同点是"后台在跑、界面只是入口"，所以状态集中放在 v127State 里，
// 不再往 App 结构体上摊十几个字段。所有跨线程访问（HTTP 回调、守护 goroutine、
// 更新检查）都经过 channel 或 mutex，不让后台线程直接摸界面状态。

const (
	v127TickGap   = 5 * time.Second  // 主循环里的调度节拍
	watchPollGap  = 15 * time.Second // 目录快照轮询间隔
	remoteMaxText = 4000             // 远程指令长度上限（与 remote 包一致）
	pushMaxRunes  = 1200             // 推给手机的助手回复截断长度
)

type v127State struct {
	// 手机指挥台
	rctl    *remote.Server
	remoteQ []string // 待执行的远程指令（界面线程消费）
	rqMu    sync.Mutex
	pushed  int // 已推送到手机的消息条数

	// 定时任务
	sched   *core.ScheduleStore
	schedMu sync.Mutex

	// 目录守护
	watch   *core.WatchStore
	watchMu sync.Mutex
	watchCh chan string

	// 更新检查
	updCh   chan string
	updOnce bool

	lastTick time.Time

	// 并列视图（/compare）
	cmpRight int
}

// initV127 在应用启动时装配 v1.27 能力。任何一项装配失败都不影响主界面：
// 定时任务文件坏了就当天没有定时任务，而不是开不了程序。
func (a *App) initV127() {
	st := &v127State{
		watchCh: make(chan string, 16),
		updCh:   make(chan string, 4),
	}
	st.sched = core.LoadSchedules(core.SchedulesPath())
	st.watch = core.LoadWatches(core.WatchesPath())
	st.rctl = remote.NewServer()
	st.rctl.SetStatus(a.remoteStatus)
	st.rctl.SetSubmit(func(c remote.Command) {
		// HTTP goroutine 里只入队：直接改界面状态会与绘制线程抢数据。
		st.rqMu.Lock()
		st.remoteQ = append(st.remoteQ, c.Text)
		st.rqMu.Unlock()
	})
	a.v27 = st

	if a.cfg.RemoteEnabled {
		if _, err := a.startRemote(); err != nil {
			a.toast("手机指挥台启动失败："+err.Error(), true)
		}
	}
	go a.watchLoop()
	manifestMode := strings.EqualFold(strings.TrimSpace(a.cfg.UpdateSource), core.UpdateSourceManifest)
	updateConfigured := (manifestMode && strings.TrimSpace(a.cfg.UpdateURL) != "") ||
		(!manifestMode && strings.TrimSpace(a.cfg.UpdateRepo) != "")
	if a.cfg.CheckUpdateOnStart && updateConfigured {
		go a.checkUpdateQuiet()
	}
}

// sendText 以程序方式发一轮对话（定时任务、远程指令、守护触发共用）。
// 复用 send() 而不是另写一份，是为了让这些"非人手输入"的消息同样进入
// 上下文、日志与断点记录，出问题时能按同一条链路排查。
func (a *App) sendText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || a.streaming || a.eng == nil {
		return false
	}
	a.input = []rune(text)
	a.inputCur = len(a.input)
	if a.v29 != nil {
		a.v29.turnAt = time.Now()
	}
	a.send()
	return true
}

// tickV127 由 33fps 的主循环调用，内部按 5 秒节流。
func (a *App) tickV127(now time.Time) {
	st := a.v27
	if st == nil {
		return
	}
	a.drainRemote(now)
	if now.Sub(st.lastTick) < v127TickGap {
		return
	}
	st.lastTick = now

	// 定时任务：v1.28 起交给 catchupSchedules 处理。
	// 它比这里的老循环多两件事：认得出"开机前就错过的那次"，
	// 以及漏跑多次时只补一次但明确告诉用户漏了几次。
	a.catchupSchedules(now)

	// 助手回复推给手机：只在非流式时推完整消息，避免把半句话发过去。
	if st.rctl != nil && st.rctl.Running() && !a.streaming && len(a.msgs) > st.pushed {
		for i := st.pushed; i < len(a.msgs); i++ {
			m := a.msgs[i]
			if m.Role != "assistant" || strings.TrimSpace(m.Text) == "" {
				continue
			}
			st.rctl.PushEvent("reply", clipRunes(m.Text, pushMaxRunes))
		}
		st.pushed = len(a.msgs)
	}
}

// drainRemote 把手机投过来的指令交给引擎。忙时留在队列里等，
// 不丢弃也不插队——人在手机上按的那一下，不该打乱正在跑的活。
func (a *App) drainRemote(now time.Time) {
	st := a.v27
	if st == nil || a.streaming || a.eng == nil {
		return
	}
	for {
		st.rqMu.Lock()
		if len(st.remoteQ) == 0 {
			st.rqMu.Unlock()
			return
		}
		text := st.remoteQ[0]
		st.rqMu.Unlock()
		if !a.sendText(text) {
			return
		}
		st.rqMu.Lock()
		if len(st.remoteQ) > 0 {
			st.remoteQ = st.remoteQ[1:]
		}
		st.rqMu.Unlock()
	}
}

// remoteStatus 供指挥台展示的状态。
func (a *App) remoteStatus() remote.Status {
	return remote.Status{
		Version: core.Version,
		Model:   a.convModel(),
		WorkDir: a.convWorkDir(),
		Busy:    a.streaming,
		Note:    a.statusMsg,
	}
}

// startRemote 按当前配置启动监听，返回可访问地址。
func (a *App) startRemote() (string, error) {
	st := a.v27
	if st == nil || st.rctl == nil {
		return "", fmt.Errorf("控制台未初始化")
	}
	listen := remote.ListenAddr(a.cfg.RemoteLAN, a.cfg.RemotePort)
	if a.cfg.RemoteHTTPS {
		// 自签证书现生成现用：每台机器各自签一份，不共用私钥。
		certPEM, keyPEM, err := remote.LoadOrCreateSelfSignedCert(remote.CertHosts(), remote.CertDays)
		if err != nil {
			return "", fmt.Errorf("生成自签证书失败：%w", err)
		}
		return st.rctl.StartHTTPS(listen, certPEM, keyPEM)
	}
	return st.rctl.Start(listen)
}

// stopRemote 停止监听。
func (a *App) stopRemote() {
	if a.v27 != nil && a.v27.rctl != nil {
		_ = a.v27.rctl.Stop()
	}
}

// watchLoop 后台轮询目录变化。快照遍历是磁盘 IO，绝不能放在绘制线程里：
// 一次全仓遍历在老机器上要几百毫秒，放在界面线程就是肉眼可见的卡顿。
func (a *App) watchLoop() {
	tk := time.NewTicker(watchPollGap)
	defer tk.Stop()
	for range tk.C {
		st := a.v27
		if st == nil {
			continue
		}
		st.watchMu.Lock()
		rules := append([]*core.WatchRule(nil), st.watch.Items...)
		var fired []string
		for _, r := range rules {
			if !r.Enabled {
				continue
			}
			if ok, diff := st.watch.Poll(r, time.Now()); ok {
				fired = append(fired, fmt.Sprintf("[目录守护] %s 有 %d 处变化：%s\n任务：%s",
					r.Path, diff.Total(), diff.ChangeSummary(), r.Prompt))
			}
		}
		if len(fired) > 0 {
			_ = st.watch.Save()
		}
		st.watchMu.Unlock()
		for _, msg := range fired {
			select {
			case st.watchCh <- msg:
			default:
				// 通道满说明界面长时间没消费，宁可丢这一次也不要阻塞轮询
			}
		}
	}
}

// drainWatch 在界面线程消费守护事件。
func (a *App) drainWatch() {
	st := a.v27
	if st == nil {
		return
	}
	for {
		select {
		case msg := <-st.watchCh:
			if a.streaming || a.eng == nil {
				a.sysNote(msg + "\n\n（引擎正在忙，这条守护提醒没有自动执行，需要时手动发一次）")
				continue
			}
			if !a.sendText(msg) {
				a.sysNote(msg)
			}
		default:
			return
		}
	}
}

// checkUpdateQuiet 启动时静默查一次更新，只提示不下载。
func (a *App) checkUpdateQuiet() {
	st := a.v27
	if st == nil {
		return
	}
	uc, newer, err := core.CheckUpdateAny(core.Version, a.cfg.UpdateSource, a.cfg.UpdateRepo,
		strings.TrimSpace(a.cfg.UpdateURL), core.HTTPFetcher)
	if err != nil || !newer {
		return
	}
	select {
	case st.updCh <- fmt.Sprintf("发现新版本 %s（当前 %s）。\n%s\n更新：/update 查看详情", uc.Version, core.Version, uc.Notes):
	default:
	}
	if a.cfg.AutoDownloadUpdate {
		go a.autoDownloadUpdate(uc)
	}
}

// autoDownloadUpdate 后台下载已校验的更新包并暂存，不替换正在运行的 exe。
func (a *App) autoDownloadUpdate(uc *core.UpdateCheck) {
	if uc == nil || uc.File.URL == "" {
		return
	}
	if err := core.PrepareUpdate(core.UpdatesDir()); err != nil {
		a.notify("更新包下载失败", err.Error(), true)
		return
	}
	newExe, err := core.DownloadUpdate(uc.File, runtime.GOARCH, core.HTTPFetcher)
	if err != nil {
		a.notify("更新包下载失败", err.Error(), true)
		return
	}
	if want := core.ExeArchOfSelf(); want != "" {
		if err := core.CheckUpdateArch(newExe, want); err != nil {
			a.notify("更新包架构不匹配", err.Error(), true)
			return
		}
	}
	_ = core.StageUpdate(core.UpdatesDir(), newExe)
	a.notify("更新包已就绪", "版本 "+uc.Version+" 已通过 SHA256 与架构校验；重启后运行 /update apply 安装。", false)
	core.Logf("auto-update: 已暂存 %s (%s)", newExe, uc.Version)
}

// drainUpdate 把更新提示转成界面上的系统消息。
func (a *App) drainUpdate() {
	st := a.v27
	if st == nil {
		return
	}
	for {
		select {
		case msg := <-st.updCh:
			a.sysNote(msg)
		default:
			return
		}
	}
}

// clipRunes 按字符数截断，避免把多字节汉字截成乱码。
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…（已截断）"
}

// textScale 内容字号缩放系数（1.0 = 100%）。
// 只作用于对话内容排版，工具栏与按钮保持原尺寸——按钮变大在 1024x768 上
// 会把整行挤到换行，反而更难用。
func (a *App) textScale() float32 {
	f := a.cfg.FontScale
	if f <= 0 {
		return 1
	}
	s := float32(f) / 100
	if s < 0.8 {
		return 0.8
	}
	if s > 2 {
		return 2
	}
	return s
}
