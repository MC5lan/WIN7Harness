//go:build windows

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"win7harness/internal/bootstrap"
	"win7harness/internal/core"
)

// cmdDoctor 诊断当前会话的端点（/models + 最短对话 + 工具调用）。
func (a *App) cmdDoctor() {
	p := a.convProvider()
	a.doctorWith(p, a.convModel())
}

// cmdDoctorForm 诊断设置页当前表单里的端点；用户不必先保存再诊断。
func (a *App) cmdDoctorForm() {
	if a.ui == nil {
		a.cmdDoctor()
		return
	}
	p := core.Provider{
		BaseURL: strings.TrimSpace(a.ui.Field("set.base", "").String()),
		APIKey:  strings.TrimSpace(a.ui.Field("set.key", "").String()),
		Model:   strings.TrimSpace(a.ui.Field("set.model", "").String()),
	}
	if ap := a.cfg.Provider(); ap.NoAuth || ap.Local {
		p.NoAuth = ap.NoAuth
		p.Local = ap.Local
	}
	model := p.Model
	if p.BaseURL == "" {
		p = a.convProvider()
	}
	if model == "" {
		model = a.convModel()
	}
	a.doctorWith(p, model)
}

func (a *App) doctorWith(p core.Provider, model string) {
	if a.doctorBusy {
		a.toast("端点诊断正在进行，稍等", false)
		return
	}
	if core.PortableSecretsLocked() && strings.TrimSpace(p.APIKey) == "" {
		a.toast("先解锁 U 盘密钥，再诊断端点", true)
		return
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		a.toast("先填接口地址 Base URL", true)
		return
	}
	if strings.TrimSpace(p.APIKey) == "" && !p.NoAuth && !p.Local {
		a.toast("先填 API Key，或选择本地模型", true)
		return
	}
	if model == "" {
		model = p.Model
	}
	if model == "" {
		a.toast("先填模型 ID，再诊断", true)
		return
	}

	client, used, warn := bootstrap.NewClientFor(a.cfg, p, model)
	if client == nil {
		a.toast("诊断客户端创建失败", true)
		return
	}
	if used != "" {
		model = used
	}
	if warn != "" {
		a.toast(warn, true)
	}
	if cv := a.currentConv(); cv != nil && cv.ID != "" {
		client.SessionID = "win7h-" + cv.ID
	}

	a.doctorBusy = true
	a.toast("正在诊断端点（会发一条最短对话，约几个 token）…", false)
	core.SafeGo("app/doctor.go:doctor", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		rep := client.Doctor(ctx)
		text := rep.Text()
		if dir := filepath.Join(core.LocalDataDir(), "doctor"); os.MkdirAll(dir, 0o755) == nil {
			name := "doctor-" + time.Now().Format("20060102-150405") + ".txt"
			path := filepath.Join(dir, name)
			if err := core.WriteFileAtomic(path, []byte(text), 0o644); err == nil {
				text += "\r\n报告：" + path
			}
		}
		a.doctorCh <- testResult{msg: text, ok: rep.OK()}
	})
}
