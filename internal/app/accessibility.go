//go:build windows

package app

import (
	"fmt"
	"math"
	"strings"

	"win7harness/internal/ui"
)

// AccessibleItems 为 WM_GETOBJECT 提供当前界面的语义快照。
// 自定义绘制的聊天没有子控件，这里把最关键的 3 个区域暴露成 MSAA 虚拟子项：
// 对话正文、输入框、发送/停止按钮。文本读取不依赖具体像素布局。
func (a *App) AccessibleItems() []ui.AccessibleItem {
	if a == nil {
		return nil
	}
	scale := float32(1)
	if a.w != nil && a.w.Scale() > 0 {
		scale = a.w.Scale()
	}
	px := func(v int32) int32 { return int32(math.Round(float64(v) * float64(scale))) }
	winW, winH := a.winW, a.winH
	if winW <= 0 {
		winW = 960
	}
	if winH <= 0 {
		winH = 700
	}

	// evMu 可能正被流式事件占用；TryLock 失败时仍给出输入/按钮，
	// 不阻塞 UI 线程，也不读半更新的 msgs。
	gotLock := a.evMu.TryLock()
	if gotLock {
		defer a.evMu.Unlock()
	}
	chatText := "（对话正在更新，请稍后重新读取）"
	if gotLock {
		msgs := a.msgs
		if len(msgs) == 0 && a.active >= 0 && a.active < len(a.convs) {
			msgs = a.convs[a.active].Msgs
		}
		var chat strings.Builder
		start := 0
		if len(msgs) > 60 {
			start = len(msgs) - 60
		}
		for _, m := range msgs[start:] {
			role := "用户"
			if m.Role == "assistant" {
				role = "助手"
			}
			text := strings.TrimSpace(m.Text)
			if text == "" {
				if m.Streaming {
					text = "（正在生成）"
				} else {
					continue
				}
			}
			if m.Streaming {
				text += "（正在生成）"
			}
			fmt.Fprintf(&chat, "%s：%s\n\n", role, text)
			if chat.Len() > 64<<10 {
				break
			}
		}
		chatText = strings.TrimSpace(chat.String())
		if chatText == "" {
			chatText = "（当前没有对话内容）"
		}
	}

	inputText := string(a.input)
	inputFocused := a.ui != nil && a.ui.Focus() == "input"
	inputRect := a.inputRect
	if inputRect.W <= 0 {
		inputRect = ui.Rect{X: 0, Y: winH - 120, W: winW, H: 100}
	}
	sendW, sendH := int32(56), int32(32)
	sendX := inputRect.X + inputRect.W - sendW - 8
	sendY := inputRect.Y + inputRect.H - sendH - 8
	if sendX < 0 {
		sendX = winW - sendW - 12
	}
	if sendY < 0 {
		sendY = winH - sendH - 12
	}
	sendName := "发送"
	if gotLock && a.streaming {
		sendName = "停止"
	}

	chatState := uint32(ui.StateSystemFocusable | ui.StateSystemReadOnly)
	inputState := uint32(ui.StateSystemFocusable)
	if inputFocused {
		inputState |= ui.StateSystemFocused
	}
	return []ui.AccessibleItem{
		{
			Name:        "对话记录",
			Value:       chatText,
			Description: "当前会话的可见消息正文",
			Role:        ui.RoleSystemDocument,
			State:       chatState,
			X:           0,
			Y:           0,
			W:           px(winW),
			H:           px(winH),
		},
		{
			Name:        "输入框",
			Value:       inputText,
			Description: "输入消息；按 Enter 发送",
			Role:        ui.RoleSystemText,
			State:       inputState,
			X:           px(inputRect.X),
			Y:           px(inputRect.Y),
			W:           px(inputRect.W),
			H:           px(inputRect.H),
			Focused:     inputFocused,
		},
		{
			Name:          sendName,
			Description:   "发送当前输入内容",
			DefaultAction: "按下",
			Role:          ui.RoleSystemPushButton,
			State:         ui.StateSystemFocusable,
			X:             px(sendX),
			Y:             px(sendY),
			W:             px(sendW),
			H:             px(sendH),
		},
	}
}
