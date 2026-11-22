//go:build windows

package app

import (
	"fmt"
	"strings"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/ui"
)

// —— v1.28：指挥台 2.0 的界面侧接线 + 稳定性补强 ——
//
// 这一版的所有"智能"都在 core / remote 包里做完了，这里只负责三件事：
//  1. 把配置与运行期状态接到那些包里（会话目录、审批通道、HTTPS）
//  2. 在窗口上画二维码、弹托盘气泡
//  3. 失败之后自动留证据（诊断包），别让人回头翻日志

const (
	// 同一个失败原因 10 分钟内只生成一次诊断包：
	// 模型连不上时往往每几秒失败一次，每次都打包会把磁盘和用户耐心一起耗光。
	autoDiagGap = 10 * time.Minute
	// 二维码面板自动消失时间。放在屏幕上不动会被误以为是待办事项。
	qrPanelLife = 5 * time.Minute
)

// initV128 在 v1.27 能力装配完成后调用。
func (a *App) initV128() {
	st := a.v27
	if st == nil || st.rctl == nil {
		return
	}

	// 会话目录：手机上要能看到"电脑上正在聊什么、跑到哪了"。
	st.rctl.SetSessionDir(core.SessionsDir())

	// 审批回传：默认开（用户开指挥台基本就是想要这个），但显式配过就尊重配置。
	approval := true
	if a.cfg.RemoteApprovalSet {
		approval = a.cfg.RemoteApproval
	}
	st.rctl.SetApprovalHub(approval)

	// HTTPS 由 startRemote 读取配置决定；这里只保证切换时的一致性。
	if a.cfg.RemoteHTTPS {
		st.rctl.SetHTTPS(true)
	}
}

// remoteApprovalOn 供状态展示用的当前口径。
func (a *App) remoteApprovalOn() bool {
	if a.cfg.RemoteApprovalSet {
		return a.cfg.RemoteApproval
	}
	return true
}

// setRemoteApproval 修改手机审批开关并落盘。
func (a *App) setRemoteApproval(on bool) {
	a.cfg.RemoteApproval = on
	a.cfg.RemoteApprovalSet = true
	a.cfg.Save()
	if a.v27 != nil && a.v27.rctl != nil {
		a.v27.rctl.SetApprovalHub(on)
	}
}

// trayEnsure 首次把图标挂到托盘。窗口创建后才能做，所以放在主循环里懒执行。
func (a *App) trayEnsure() {
	if a.trayInit || a.w == nil {
		return
	}
	if ok := a.w.EnableTray("Win7 Harness"); ok {
		a.trayInit = true
		a.trayOn = true
		a.w.SetTrayClick(func() {
			a.toast("窗口已从托盘恢复", false)
		})
		core.Logf("托盘图标已添加")
		return
	}
	// Explorer 可能还没起来（刚登录、远程桌面切换、资源管理器重启），
	// Shell_NotifyIconW 会静默失败。每帧重试，超过一定次数再放弃并写日志。
	a.trayFails++
	if a.trayFails >= 120 {
		a.trayInit = true
		core.Logf("!! 托盘图标添加失败 %d 次，已放弃；可手动重启资源管理器后重试", a.trayFails)
	}
}

// notify 桌面通知。优先气泡，托盘不可用时退回界面内提示。
// 这条通道专给"人不看屏幕时也该知道"的事件：任务完成、失败、定时触发。
func (a *App) notify(title, text string, isErr bool) {
	if a.w != nil && a.trayOn {
		if a.w.Notify(title, text, isErr) {
			return
		}
	}
	a.toast(title+" "+text, isErr)
}

// autoDiag 任务失败后自动导出诊断包（含脱敏配置、最近日志、环境信息）。
// 只在真的失败时调用，且做了冷却。返回生成的路径，未生成时为空。
func (a *App) autoDiag(reason string) string {
	if a.eng == nil {
		return ""
	}
	now := time.Now()
	if !a.lastDiag.IsZero() && now.Sub(a.lastDiag) < autoDiagGap {
		return ""
	}
	a.lastDiag = now
	opt := core.DefaultDiagOptions()
	opt = a.withMetricsExtra(opt)
	path, err := core.ExportDiag("", opt)
	if err != nil || path == "" {
		return ""
	}
	a.sysNote("这次失败已经自动存了一份诊断包（配置已脱敏）：\r\n" + path + "\r\n/rbug 可直接打开所在目录。")
	return path
}

// showQR 生成配对二维码并显示在窗口中央。
func (a *App) showQR() {
	st := a.v27
	if st == nil || st.rctl == nil {
		a.sysNote("手机指挥台未初始化。")
		return
	}
	if !st.rctl.Running() {
		a.sysNote("指挥台还没开。先 /remote on")
		return
	}
	url := st.rctl.PairURL()
	q, err := core.EncodeQR(url)
	if err != nil {
		a.sysNote("二维码生成失败：" + err.Error() + "\r\n直接用配对码：" + st.rctl.PairCode())
		return
	}
	a.qr = q
	a.qrURL = url
	a.qrUntil = time.Now().Add(qrPanelLife)
	a.Redraw()
}

// closeQR 收起二维码面板。
func (a *App) closeQR() {
	a.qr = nil
	a.qrURL = ""
	a.qrUntil = time.Time{}
	a.Redraw()
}

func (a *App) qrVisible() bool {
	if a.qr == nil {
		return false
	}
	if time.Now().After(a.qrUntil) {
		a.qr = nil
		return false
	}
	return true
}

// paintQR 画二维码面板。二维码本身用方块拼，不做抗锯齿：
// 缩放模糊会让老手机的对焦算法认不出来，反而是清晰的方块最好扫。
func (a *App) paintQR(c *ui.Canvas) {
	if !a.qrVisible() {
		return
	}
	th := a.th
	q := a.qr
	c.FillRect(ui.R(0, 0, a.winW, a.winH), 0x0A0C10)

	// 面板尺寸按二维码大小定，左右各留 28 的内边距
	cell := int32(6)
	if q.Size > 33 {
		cell = 5
	}
	side := int32(q.Size)*cell + 56
	w := side
	// 面板宽度不能只按二维码算：地址行是 http://192.168.20.18:8765/?code=845315
	// 这种长度，只按二维码定宽会让地址冲出右边界（看着像文字叠在面板外）。
	addr := "地址：" + a.qrURL
	if need := c.MeasureFace(addr, ui.FsXs, 400, ui.FaceMono) + 56; need > w {
		w = need
	}
	h := side + 96
	x := (a.winW - w) / 2
	y := (a.winH - h) / 2
	r := ui.R(x, y, w, h)
	c.Round(r, 10, th.Surface)
	c.RoundStroke(r, 10, th.Border)

	c.TextFace(x+28, y+20, "扫码配对手机指挥台", ui.FsLg, 700, th.Text, ui.FaceUI)
	c.TextFace(x+28, y+48, "手机相机扫一下，自动填码并完成配对（5 分钟内有效）", ui.FsXs, 400, th.TextMute, ui.FaceUI)

	ox := x + 28
	oy := y + 72
	// 白底 + 四周 4 个模块的静区，缺了静区扫码器扫不出来
	c.FillRect(ui.R(ox, oy, int32(q.Size)*cell+32, int32(q.Size)*cell+32), 0xFFFFFF)
	for yy, row := range q.Modules {
		for xx, v := range row {
			if !v {
				continue
			}
			c.FillRect(ui.R(ox+16+int32(xx)*cell, oy+16+int32(yy)*cell, cell, cell), 0x000000)
		}
	}

	c.TextFace(x+28, y+h-46, ellipsis(c, addr, ui.FsXs, 400, w-56), ui.FsXs, 400, th.TextDim, ui.FaceMono)
	c.TextFace(x+28, y+h-26, "Esc 或点击任意处关闭", ui.FsXs, 400, th.TextMute, ui.FaceUI)
}

// tickV128 每分钟级别的收尾工作：托盘、二维码过期、定时补跑提示。
func (a *App) tickV128(now time.Time) {
	a.trayEnsure()
	if a.qr != nil && now.After(a.qrUntil) {
		a.qr = nil
		a.Redraw()
	}
}

// catchupSchedules 处理"到点但错过了"的定时任务。
// 与 v1.27 的区别：不但要跑，还要告诉用户"这条其实错过了几次"。
// 机器关机、睡眠、引擎长时间占用都会造成这种漏跑，静默跳过才是真的坑。
func (a *App) catchupSchedules(now time.Time) int {
	st := a.v27
	if st == nil || a.streaming || a.eng == nil {
		return 0
	}
	st.schedMu.Lock()
	items := st.sched.DueCatchup(now)
	st.schedMu.Unlock()

	fired := 0
	queued := 0
	for _, it := range items {
		prompt := it.Schedule.Prompt
		if it.Missed > 1 {
			// 补跑只跑一次，但要把"漏了几次"讲清楚，
			// 否则用户会以为这个任务一直在正常执行。
			prompt = fmt.Sprintf("（补跑说明：本定时任务在机器关机或繁忙期间错过了 %d 次，这里只补最近一次，不要重复执行。）\n%s",
				it.Missed, it.Schedule.Prompt)
		}
		if a.streaming || a.eng == nil || !a.sendText(prompt) {
			// v1.29：忙的时候不再 break 丢弃，而是排队等空闲。
			// 用户排的定时任务被悄悄吞掉，比晚几分钟跑糟糕得多。
			dedup := "sched:" + it.Schedule.ID + "@" + now.Format("20060102T1504")
			if a.enqueueTask(prompt, dedup, "定时任务") {
				st.schedMu.Lock()
				st.sched.MarkRun(it.Schedule.ID, now)
				st.schedMu.Unlock()
				queued++
			}
			continue
		}
		st.schedMu.Lock()
		st.sched.MarkRun(it.Schedule.ID, now)
		st.schedMu.Unlock()
		fired++
		if it.Missed > 1 {
			a.sysNote(fmt.Sprintf("定时任务补跑：%s（错过了 %d 次，已合并为一次）",
				clipRunes(it.Schedule.Prompt, 40), it.Missed))
		} else {
			a.toast("定时任务已触发："+it.Schedule.Describe(), false)
		}
	}
	if fired > 0 || queued > 0 {
		st.schedMu.Lock()
		_ = st.sched.Save()
		st.schedMu.Unlock()
	}
	if fired > 0 {
		a.notify("定时任务", fmt.Sprintf("已触发 %d 个定时任务", fired), false)
	}
	if queued > 0 {
		a.toast(fmt.Sprintf("有 %d 个定时任务在排队（当前正忙，跑完自动接上）", queued), false)
	}
	return fired
}

// startRemoteSecure 按配置启动监听（HTTP 或 HTTPS）。
func (a *App) startRemoteSecure() (string, error) {
	st := a.v27
	if st == nil || st.rctl == nil {
		return "", fmt.Errorf("控制台未初始化")
	}
	st.rctl.SetHTTPS(a.cfg.RemoteHTTPS)
	addr := st.rctl.BaseURL()
	_ = addr
	return a.startRemote()
}

// remoteScheme 给用户展示的协议前缀。
func (a *App) remoteScheme() string {
	if a.cfg.RemoteHTTPS {
		return "https"
	}
	return "http"
}

// qrHint 一句话说明"怎么让手机进来"，用在 /remote 的状态输出里。
func (a *App) qrHint() string {
	if a.v27 == nil || a.v27.rctl == nil || !a.v27.rctl.Running() {
		return ""
	}
	return "扫码配对：/remote qr　（手机与电脑同一 Wi-Fi）"
}

// remoteSecurityLine 描述当前连接的安全等级，避免用户误以为局域网就绝对安全。
func (a *App) remoteSecurityLine() string {
	if a.cfg.RemoteHTTPS {
		return "传输：HTTPS（自签证书，手机首次访问需点「继续」）"
	}
	return "传输：HTTP 明文，同网段可被嗅探；介意就用 /remote https on 开加密"
}

// diagQuick 供 /diag 使用的"手动打一份"入口（带上当前失败上下文）。
func (a *App) diagQuick(reason string) string {
	path, err := core.ExportDiag("", core.DefaultDiagOptions())
	if err != nil {
		a.sysNote("诊断包生成失败：" + err.Error())
		return ""
	}
	return path
}

// remoteStatusLine 在标题栏状态里体现"手机正在连着"这件事。
// 用久了会忘记自己开过指挥台，这样至少每次看标题都能想起来。
func (a *App) remoteStatusLine() string {
	st := a.v27
	if st == nil || st.rctl == nil || !st.rctl.Running() {
		return ""
	}
	u := a.remoteScheme() + "://" + st.rctl.Addr()
	if a.remoteApprovalOn() {
		return "指挥台 " + u + " · 手机可审批"
	}
	return "指挥台 " + u
}

// 小工具：把多行文本压成一行用于标题/气泡。
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	if len([]rune(s)) > 120 {
		r := []rune(s)
		s = string(r[:120]) + "…"
	}
	return strings.TrimSpace(s)
}

// lastTextLine 取正文最后一行非空内容，气泡里只放得下一句话。
func lastTextLine(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return s
}
