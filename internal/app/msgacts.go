//go:build windows

package app

// 消息级与工具卡级交互：复制、重试、编辑重发、删除会话确认。
// 这些入口此前只能靠快捷键或命令（Ctrl+V、/image、/undo）触达，
// 界面上没有可点的按钮，属于"看不见就算没有"的缺陷，统一在这里补齐。

import (
	"fmt"
	"strings"

	"win7harness/internal/win32"
)

// copyMsg 复制某条消息的正文到剪贴板。
func (a *App) copyMsg(i int) {
	if i < 0 || i >= len(a.msgs) {
		return
	}
	m := a.msgs[i]
	text := m.Text
	if strings.TrimSpace(text) == "" && len(m.Images) > 0 {
		text = strings.Join(m.Images, "\r\n")
	}
	if strings.TrimSpace(text) == "" {
		a.toast("这条消息没有可复制的文本", true)
		return
	}
	if win32.SetClipboardText(text) {
		a.toast("已复制到剪贴板", false)
	} else {
		a.toast("复制失败：剪贴板被占用", true)
	}
}

// copyToolOutput 复制工具卡里的完整输出（不是界面上截断过的预览）。
func (a *App) copyToolOutput(mi, ti int) {
	if mi < 0 || mi >= len(a.msgs) || ti < 0 || ti >= len(a.msgs[mi].Tools) {
		return
	}
	out := a.msgs[mi].Tools[ti].Output
	if strings.TrimSpace(out) == "" {
		a.toast("这次调用没有输出", true)
		return
	}
	if win32.SetClipboardText(out) {
		a.toast("已复制工具输出", false)
	} else {
		a.toast("复制失败：剪贴板被占用", true)
	}
}

// retryFrom 重新生成第 i 条助手回复。只支持最后一条，
// 否则引擎上下文与界面消息会错位。
func (a *App) retryFrom(i int) {
	if a.eng == nil {
		a.toast("引擎未就绪", true)
		return
	}
	if a.streaming {
		a.toast("正在生成，稍后再试", true)
		return
	}
	if i < 0 || i >= len(a.msgs) {
		return
	}
	if i != len(a.msgs)-1 {
		a.toast("只能重试最后一条回复", true)
		return
	}
	j := i - 1
	for j >= 0 && a.msgs[j].Role != "user" {
		j--
	}
	if j < 0 {
		a.toast("没有可重试的用户消息", true)
		return
	}
	if !a.eng.Retry() {
		a.toast("引擎忙，稍后再试", true)
		return
	}
	// 先备份旧回复：重试再次失败时由 onEvent 放回，避免永久丢答案。
	old := a.msgs[i]
	a.retryBackup = &old
	a.msgs = a.msgs[:i]
	a.curAsst = -1
	a.streaming = true
	a.autoScroll = true
	a.ver++
	a.saveConv()
	a.scrollToBottom()
}

// editMsg 把某条用户消息的内容填回输入框，改完回车重发。
func (a *App) editMsg(i int) {
	if i < 0 || i >= len(a.msgs) {
		return
	}
	m := a.msgs[i]
	if m.Role != "user" {
		return
	}
	if a.streaming {
		a.toast("正在生成，稍后再试", true)
		return
	}
	a.input = []rune(m.Text)
	a.inputCur = len(a.input)
	a.ver++
	a.ui.SetFocus("input")
	a.toast("已填入输入框，改完回车发送", false)
}

// requestDeleteConv 删除会话前先确认：会话文件删掉不可恢复。
func (a *App) requestDeleteConv(i int) {
	if i < 0 || i >= len(a.convs) {
		return
	}
	cv := a.convs[i]
	cvID := cv.ID
	title := cv.Title
	if strings.TrimSpace(title) == "" {
		title = "新对话"
	}
	count := len(cv.Msgs)
	if !cv.Loaded {
		count = cv.MsgN
	}
	a.modal = &Modal{
		Title:   "删除会话",
		Detail:  fmt.Sprintf("将删除「%s」（%d 条消息）及其本地记录，此操作不可撤销。", title, count),
		Confirm: true,
		OnOK:    func() { a.deleteConvByID(cvID) },
	}
	a.ver++
}
