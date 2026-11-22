//go:build windows

package app

import (
	"testing"

	"win7harness/internal/ui"
)

// 回归：模态确认框的「允许执行」鼠标点击必须有效。
// 过去只依赖上一帧注册的命中区，某些绘制时序下 HitAt 会拿到下层控件，
// 用户点了没反应。modalClick 现在用按钮几何再判一次，这里把它钉死。
func TestModalClickOKGeometryFallback(t *testing.T) {
	th := ui.LightTheme()
	called := false
	a := &App{
		th:    th,
		ui:    NewUI(nil, th),
		winW:  1280,
		winH:  720,
		modal: &Modal{Kind: modalConfirm, OnOK: func() { called = true }},
	}
	r := a.modalRect()
	ok := ui.R(r.Right()-118, r.Bottom()-60, 94, 34)

	// 不注册任何命中区，模拟命中列表丢失；几何兜底仍应派发确认。
	a.modalClick(ok.CX(), ok.CY())
	if !called {
		t.Fatal("点击「允许执行」没有触发 OnOK")
	}
	if a.modal != nil {
		t.Fatal("确认后弹窗没有关闭")
	}
}

func TestModalClickCancelGeometryFallback(t *testing.T) {
	th := ui.LightTheme()
	called := false
	a := &App{
		th:    th,
		ui:    NewUI(nil, th),
		winW:  1280,
		winH:  720,
		modal: &Modal{Kind: modalConfirm, OnOK: func() { called = true }},
	}
	r := a.modalRect()
	cancel := ui.R(r.Right()-220, r.Bottom()-60, 94, 34)
	a.modalClick(cancel.CX(), cancel.CY())
	if called {
		t.Fatal("点击「取消」不应触发 OnOK")
	}
	if a.modal != nil {
		t.Fatal("取消后弹窗没有关闭")
	}
}
