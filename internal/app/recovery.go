package app

import (
	"fmt"
	"strings"
	"time"

	"win7harness/internal/core"
)

// 崩溃 / 断电恢复的界面侧接线。
//
// 分工：core.Journal 负责落盘与判读，App 负责"什么时候记、怎么告诉用户"。
// 时机上刻意选了三个点：发消息时（开始）、每个工具结束时（进度）、
// 轮次结束或出错时（收尾）。这样掉电最多丢最后一个工具的结果，
// 而用户能看到的那条恢复提示是准确的。

// journal 懒初始化轮次日志；测试里 App 可能没有完整配置。
func (a *App) journal() *core.Journal {
	if a.jr == nil {
		a.jr = core.NewJournal(core.SessionsDir())
	}
	return a.jr
}

// journalBegin 在用户发消息时落一条"开始了"。
func (a *App) journalBegin(text string) {
	conv := a.currentConv()
	if conv == nil {
		return
	}
	model, wd := "", ""
	if a.eng != nil {
		model, wd = a.eng.Model, a.eng.WorkDir
	}
	a.journal().Begin(core.JournalEntry{
		ConvID:    conv.ID,
		UserText:  text,
		Model:     model,
		WorkDir:   wd,
		StartedAt: time.Now(),
	})
}

// journalProgress 记录工具级进度。
// 写日志失败不影响对话：恢复能力是增强项，不能反过来卡住主流程。
func (a *App) journalProgress(tool string) {
	conv := a.currentConv()
	if conv == nil {
		return
	}
	round := 0
	if a.eng != nil {
		round = a.eng.CurrentRound()
	}
	a.journal().Progress(conv.ID, round, tool)
}

// journalFinish 正常收尾，删掉日志，下次启动就不会再提示恢复。
// 用户主动停止也走这里：停止是正常结束，不是中断。
func (a *App) journalFinish() {
	conv := a.currentConv()
	if conv == nil {
		return
	}
	a.journal().Finish(conv.ID)
}

// loadRecovery 启动时检查未收尾的轮次，并在对话里给出提示。
// 只提示最近的一条：断电过多次的话，一次列五条反而让人不知道从哪下手。
func (a *App) loadRecovery() {
	list, err := a.journal().Pending()
	if err != nil || len(list) == 0 {
		return
	}
	a.pendingRec = list
	// 只提示属于当前会话的那条；其它会话留到用户切过去时再说，
	// 否则一打开界面就被"你另一个对话中断了"刷屏。
	cur := a.currentConv()
	pick := list[0]
	if cur != nil {
		for _, e := range list {
			if e.ConvID == cur.ID {
				pick = e
				break
			}
		}
	}
	a.sysNote(core.RecoveryNotice(pick))
	a.recoverConv = pick.ConvID
}

// resumeLast 执行 /resume：把恢复指令交给模型，让它接着做。
func (a *App) resumeLast() {
	if a.eng == nil {
		a.sysNote("模型还没配置好，先到设置页填上端点和 Key")
		return
	}
	list, err := a.journal().Pending()
	if err != nil || len(list) == 0 {
		a.sysNote("没有检测到中断的任务。")
		return
	}
	cur := a.currentConv()
	pick := list[0]
	if cur != nil {
		found := false
		for _, e := range list {
			if e.ConvID == cur.ID {
				pick, found = e, true
				break
			}
		}
		if !found {
			a.sysNote("当前会话没有中断记录，最近一次中断在别的会话里。切到那个会话后再 /resume。")
			return
		}
	}
	if a.streaming {
		a.sysNote("正在执行，稍后再试。")
		return
	}
	a.journalBegin("【恢复中断的任务】")
	a.msgs = append(a.msgs, Message{
		Role: "user", Text: "接着上次中断的地方继续", At: time.Now(),
	})
	a.curAsst = -1
	a.streaming = true
	a.autoScroll = true
	a.ver++
	a.eng.Send(core.RecoveryPrompt(pick))
	a.saveConv()
	a.scrollToBottom()
}

// /verify 命令：查询或切换自动验证。
func (a *App) cmdVerify(rest string) {
	arg := strings.ToLower(strings.TrimSpace(rest))
	if a.cfg == nil {
		return
	}
	switch arg {
	case "":
		if a.eng != nil && a.eng.VerifyEnabled() {
			a.sysNote("自动验证：**已开启**。改完代码后会自动跑构建 + 测试，失败把摘要交回模型继续修，最多 " +
				itoa(a.eng.VerifyLimit()) + " 轮。\n\n关闭：`/verify off`")
		} else {
			a.sysNote("自动验证：**已关闭**。开启后改完代码会自动跑构建 + 测试，避免\"改完但编译不过\"直接交差。\n\n开启：`/verify on`")
		}
	case "on", "auto":
		a.cfg.VerifyMode = "auto"
		if a.cfg.Save() == nil && a.eng != nil {
			a.eng.VerifyMode = "auto"
		}
		a.sysNote("自动验证已开启：改完代码会自动跑构建 + 测试，失败最多自动修 " +
			itoa(a.eng.VerifyLimit()) + " 轮。")
	case "off":
		a.cfg.VerifyMode = "off"
		a.cfg.Save()
		if a.eng != nil {
			a.eng.VerifyMode = "off"
		}
		a.sysNote("自动验证已关闭。")
	default:
		a.sysNote("用法：`/verify` 查看，`/verify on` / `/verify off` 切换")
	}
}

// /cost 命令：成本看板。
func (a *App) cmdCost() {
	if a.eng == nil {
		a.sysNote("模型还没配置好，暂时没有可统计的消耗。")
		return
	}
	a.sysNote("```\n" + strings.TrimRight(a.eng.CostBoard(), "\n") + "\n```")
}

// itoa 小整数转字符串（避免为两处格式化引入 strconv）。
func itoa(n int) string { return fmt.Sprintf("%d", n) }
