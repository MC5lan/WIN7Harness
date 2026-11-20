//go:build windows

package app

import (
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/ui"
	"win7harness/internal/win32"
)

// maybePromptPortableUnlock 在启动后弹一次 U 盘密码框。
// 只弹一次：用户点“稍后”后，设置页里还有解锁入口，不会被反复打扰。
func (a *App) maybePromptPortableUnlock() {
	if a.pwdPrompted || a.page == PageOnboard || a.modal != nil {
		return
	}
	if !core.PortableSecretsLocked() {
		return
	}
	a.pwdPrompted = true
	a.openUnlockModal(nil)
}

// ensurePortablePassword 在要保存 API Key 前确保本次运行有 U 盘密码。
// 返回 false 表示已经弹出了密码框，调用方应直接返回，等回调再继续。
func (a *App) ensurePortablePassword(after func()) bool {
	if !core.PortableMode() || core.PortablePasswordSet() {
		return true
	}
	if core.PortableSecretsPresent() {
		a.openUnlockModal(after)
		return false
	}
	a.openSetPasswordModal(after)
	return false
}

// openSetPasswordModal 首次在 U 盘保存 Key 时设置密码。
func (a *App) openSetPasswordModal(after func()) {
	a.openPasswordModal(
		"设置 U 盘密码",
		"便携模式会把 API Key 加密保存在 U 盘里。换电脑时输入这个密码，就不用重新填 Key。",
		"密码不会保存在 U 盘上；忘记密码只能清空密钥、重新填写 API Key。",
		true,
		func(pw string) error {
			if err := core.SetPortablePassword(pw); err != nil {
				return err
			}
			a.modal = nil
			a.toast("U 盘密码已设置，请记牢", false)
			if after != nil {
				after()
			}
			return nil
		},
	)
	a.modal.PasswordCancel = func() {
		a.toast("未设置 U 盘密码，API Key 未保存", true)
	}
}

// openUnlockModal 解锁已有便携密钥。after 为空时是启动解锁，解锁后刷新表单；
// 非空时是保存前解锁，不能刷新表单，否则会把用户刚输入的 Key 覆盖掉。
func (a *App) openUnlockModal(after func()) {
	a.openPasswordModal(
		"解锁 U 盘密钥",
		"这个 U 盘上的 API Key 用 U 盘密码加密。输入密码后才能调用模型。",
		"密码只在本机内存里使用，不会写回 U 盘。",
		false,
		func(pw string) error {
			if err := core.UnlockPortableSecrets(pw); err != nil {
				return err
			}
			a.modal = nil
			a.rebuildEngine()
			if after != nil {
				after()
			} else {
				a.initForms()
				a.toast("API Key 已解锁", false)
			}
			return nil
		},
	)
	a.modal.PasswordForget = true
	a.modal.PasswordCancel = func() {
		a.toast("未解锁 U 盘密钥，调用模型前请先在设置里解锁", true)
	}
}

func (a *App) openPasswordModal(title, detail, hint string, confirm bool, ok func(string) error) {
	a.modal = &Modal{
		Kind:            modalPassword,
		Title:           title,
		Detail:          detail,
		PasswordHint:    hint,
		PasswordConfirm: confirm,
		PasswordOK:      ok,
	}
	a.modalOKFocused = false
	a.ui.SetField("modal.pwd1", "")
	a.ui.SetField("modal.pwd2", "")
	if f := a.ui.fields["modal.pwd1"]; f != nil {
		f.Masked = true
		f.Password = true
		f.Reveal = false
	}
	if f := a.ui.fields["modal.pwd2"]; f != nil {
		f.Masked = true
		f.Password = true
		f.Reveal = false
	}
	a.ui.SetFocus("modal.pwd1")
	a.Redraw()
}

func (a *App) passwordKey(vk int32) bool {
	shift := win32.GetKeyState(win32.VK_SHIFT)
	switch vk {
	case win32.VK_ESCAPE:
		a.cancelPasswordModal()
		return true
	case win32.VK_RETURN:
		a.submitPasswordModal()
		return true
	case win32.VK_TAB:
		order := []string{"modal.pwd1"}
		if a.modal != nil && a.modal.PasswordConfirm {
			order = append(order, "modal.pwd2")
		}
		cur := a.ui.Focus()
		idx := 0
		for i, id := range order {
			if id == cur {
				idx = i
				break
			}
		}
		if shift {
			idx = (idx - 1 + len(order)) % len(order)
		} else {
			idx = (idx + 1) % len(order)
		}
		a.ui.SetFocus(order[idx])
		return true
	}
	if a.ui.Focus() == "modal.pwd1" || a.ui.Focus() == "modal.pwd2" {
		if a.fieldKey(vk) {
			return true
		}
	}
	return true
}

func (a *App) submitPasswordModal() {
	m := a.modal
	if m == nil || m.Kind != modalPassword {
		return
	}
	pw := strings.TrimSpace(a.ui.Field("modal.pwd1", "").String())
	if len([]rune(pw)) < 8 {
		m.PasswordErr = "密码至少 8 个字符"
		a.Redraw()
		return
	}
	if m.PasswordConfirm {
		again := strings.TrimSpace(a.ui.Field("modal.pwd2", "").String())
		if pw != again {
			m.PasswordErr = "两次输入的密码不一致"
			a.Redraw()
			return
		}
	}
	m.PasswordErr = ""
	if m.PasswordOK != nil {
		if err := m.PasswordOK(pw); err != nil {
			m.PasswordErr = err.Error()
			a.Redraw()
			return
		}
	}
	if a.modal == m {
		a.modal = nil
		a.ui.SetFocus("")
	}
	a.Redraw()
}

func (a *App) cancelPasswordModal() {
	m := a.modal
	if m == nil || m.Kind != modalPassword {
		return
	}
	if m.PasswordCancel != nil {
		m.PasswordCancel()
	}
	a.modal = nil
	a.ui.SetFocus("")
	a.Redraw()
}

func (a *App) togglePasswordVisible() {
	if a.modal == nil || a.modal.Kind != modalPassword {
		return
	}
	for _, id := range []string{"modal.pwd1", "modal.pwd2"} {
		if f := a.ui.fields[id]; f != nil {
			f.Reveal = !f.Reveal
		}
	}
	a.Redraw()
}

func (a *App) forgetPortablePassword() {
	if a.modal == nil || a.modal.Kind != modalPassword || !a.modal.PasswordForget {
		return
	}
	if err := core.ClearPortableSecrets(); err != nil {
		a.modal.PasswordErr = err.Error()
		a.Redraw()
		return
	}
	// 把清空后的配置落盘，避免下次启动又读到旧密文。
	_ = a.cfg.Save()
	a.modal = nil
	a.ui.SetField("set.key", "")
	a.ui.SetField("ob.key", "")
	a.rebuildEngine()
	a.ui.SetFocus("")
	a.toast("已清空 U 盘密钥，请在设置里重新填写 API Key", true)
	a.Redraw()
}

func (a *App) paintPasswordModal(c *ui.Canvas, r ui.Rect, u *UI, th *ui.Theme) {
	m := a.modal
	if m == nil {
		return
	}
	// 详情最多两行；太长会挤掉输入框。
	ty := r.Y + 64
	for i, ln := range strings.Split(m.Detail, "\n") {
		if i >= 2 {
			break
		}
		c.TextFace(r.X+24, ty, ellipsis(c, ln, ui.FsXs, 400, r.W-48), ui.FsXs, 400, th.TextDim, ui.FaceUI)
		ty += 20
	}
	ty += 10

	fw := r.W - 48 - 76
	fieldW := fw
	if fieldW < 200 {
		fieldW = r.W - 48
	}
	c.TextFace(r.X+24, ty, "U 盘密码", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	ty += 18
	u.TextField("modal.pwd1", ui.R(r.X+24, ty, fieldW, 34), "", "至少 8 个字符")
	// 显示/隐藏只影响绘制，不改变真实值。
	showLabel := "显示"
	if f := u.fields["modal.pwd1"]; f != nil && f.Reveal {
		showLabel = "隐藏"
	}
	if fieldW < fw {
		u.Button("modal.pwd.show", showLabel, ui.R(r.X+24+fieldW+8, ty, 68, 34), BtnGhost)
	}
	ty += 40

	if m.PasswordConfirm {
		c.TextFace(r.X+24, ty, "再输一次", ui.FsXs, 500, th.TextDim, ui.FaceUI)
		ty += 18
		u.TextField("modal.pwd2", ui.R(r.X+24, ty, fieldW, 34), "", "确认 U 盘密码")
		ty += 40
	}

	if m.PasswordErr != "" {
		c.TextFace(r.X+24, ty, ellipsis(c, m.PasswordErr, ui.FsXs, 400, r.W-48), ui.FsXs, 400, th.Danger, ui.FaceUI)
	}
	hintY := r.Bottom() - 100
	if m.PasswordHint != "" {
		c.TextFace(r.X+24, hintY, ellipsis(c, m.PasswordHint, ui.FsXs, 400, r.W-48), ui.FsXs, 400, th.TextMute, ui.FaceUI)
	}

	by := r.Bottom() - 60
	okLabel := "解锁"
	if m.PasswordConfirm {
		okLabel = "设置密码"
	}
	if m.PasswordForget {
		if u.Button("modal.pwd.forget", "忘记密码", ui.R(r.X+24, by, 100, 34), BtnGhost) {
			a.forgetPortablePassword()
		}
	}
	if u.Button("modal.pwd.cancel", "稍后", ui.R(r.Right()-220, by, 94, 34), BtnGhost) {
		a.cancelPasswordModal()
	}
	if u.Button("modal.pwd.ok", okLabel, ui.R(r.Right()-118, by, 94, 34), BtnPrimary) {
		a.submitPasswordModal()
	}
	c.TextFace(r.X+132, by+9, "回车确认 · Esc 稍后", ui.FsXs, 400, th.TextMute, ui.FaceUI)
}
