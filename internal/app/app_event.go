//go:build windows

package app

import (
	"fmt"
	"time"
	"win7harness/internal/agent"
	"win7harness/internal/core"
	"win7harness/internal/mcp"
	"win7harness/internal/win32"
)

// 本文件由 app.go 按职责拆分（T09），仅移动代码，未改行为。
// 职责：窗口生命周期、事件循环与定时器。

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
	// 每秒发布一次配置快照：设置页/命令在 UI 线程改 a.cfg，工具 goroutine
	// 通过 core.Get() 只读 current 的深拷贝，避免并发读写同一结构体。
	if now.Sub(a.lastCfgPublish) >= time.Second {
		a.lastCfgPublish = now
		core.PublishConfig(a.cfg)
	}

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
	case "content", "reasoning", "tool_start", "tool_end":
		a.onStreamEvent(ev, m)
	case "turn_end":
		a.onTurnEnd(ev, conv, m)
	case "error":
		a.onErrorEvent(ev, m)
	case "status":
		a.onStatusEvent(ev, m)
	case "plan", "notice", "budget":
		a.onMiscEvent(ev, m)
	}
	a.ver++
	if a.autoScroll {
		a.scrollToBottom()
	}
}

// onStreamEvent 处理正文、思考与工具卡片流式事件。
func (a *App) onStreamEvent(ev agent.Event, m *Message) {
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
					m.Tools[i].Detail = ev.Detail
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
	}
}

// onTurnEnd 处理一轮正常结束。
func (a *App) onTurnEnd(ev agent.Event, conv *Conv, m *Message) {
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
}

// onErrorEvent 处理失败终态。
func (a *App) onErrorEvent(ev agent.Event, m *Message) {
	a.forceFull = true
	if m != nil {
		m.Kind = "error"
		m.Err = ev.Text
		m.Streaming = false
	} else {
		a.msgs = append(a.msgs, Message{Role: "assistant", Kind: "error", Err: ev.Text, At: time.Now()})
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
}

// onStatusEvent 处理状态栏与停止终态。
func (a *App) onStatusEvent(ev agent.Event, m *Message) {
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
}

// onMiscEvent 处理方案、通知与预算护栏。
func (a *App) onMiscEvent(ev agent.Event, m *Message) {
	switch ev.Kind {
	case "plan":
		// 审批闸门已由引擎的 PlanApprove 回调接管：事件到达时弹窗通常已经（或即将）挂起。
		// 这里只更新状态栏，绝不再覆写 a.input——那会把用户正在打的字吃掉。
		a.statusMsg = "方案待确认：在弹窗中选择批准、拒绝或修改"
	case "notice":
		a.forceFull = true
		a.msgs = append(a.msgs, Message{Role: "assistant", Kind: "notice", Text: ev.Text, At: time.Now()})
		a.autoScroll = true
		a.saveConv()
	case "budget":
		a.forceFull = true
		if m != nil {
			m.Streaming = false
		}
		a.streaming = false
		a.curAsst = -1
		a.msgs = append(a.msgs, Message{Role: "assistant", Kind: "budget", Err: ev.Text, At: time.Now()})
		a.autoScroll = true
		a.saveConv()
		a.journalFinish()
		a.toast("已触发预算护栏，停止继续调用模型", true)
	}
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
