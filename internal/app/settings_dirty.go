//go:build windows

package app

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// settingsSignature 对设置页所有可编辑字段与即时生效选项做指纹。
// 只存哈希，避免把 API Key 等敏感原文复制到另一个长期字符串。
func (a *App) settingsSignature() uint64 {
	h := fnv.New64a()
	write := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	for _, id := range []string{
		"set.base", "set.key", "set.model", "set.dir", "set.temp", "set.maxtok",
		"set.maxcost", "set.budget", "set.rounds", "set.ctxwin", "set.subto",
		"set.pricein", "set.priceout", "set.sysprompt", "set.toolprofile",
		"set.proxyurl", "set.tlsca", "set.updrepo", "set.url",
	} {
		if f := a.ui.Field(id, ""); f != nil {
			write(string(f.Text))
		}
	}
	if a.cfg != nil {
		write(a.cfg.Theme)
		write(fmt.Sprint(a.cfg.FontScale))
		write(fmt.Sprint(a.cfg.SendOnEnter))
		write(fmt.Sprint(a.cfg.ShowThinking))
		write(a.cfg.RenderMode)
		write(a.cfg.VerifyMode)
		write(fmt.Sprint(a.cfg.MaxSubTasks))
		write(fmt.Sprint(a.cfg.TLSInsecure))
		write(a.cfg.ProxyMode)
		write(fmt.Sprint(a.cfg.LowMemory))
		write(fmt.Sprint(a.cfg.NoFoldTools))
		write(a.cfg.UpdateSource)
		write(fmt.Sprint(a.cfg.CheckUpdateOnStart))
		write(fmt.Sprint(a.cfg.AutoDownloadUpdate))
	}
	return h.Sum64()
}

func (a *App) settingsDirty() bool {
	if a.ui == nil {
		return false
	}
	return a.settingsBaseline != 0 && a.settingsBaseline != a.settingsSignature()
}

// settingsMarkSaved 由保存成功路径调用，把当前表单状态记为基线。
func (a *App) settingsMarkSaved() {
	if a.ui != nil {
		a.settingsBaseline = a.settingsSignature()
	}
}

// settingsDiscardNote 返回给用户的未保存提示；空串表示没有修改。
func (a *App) settingsDiscardNote() string {
	if !a.settingsDirty() {
		return ""
	}
	return "设置页有未保存修改，已放弃本次修改"
}

// settingsDirtyLabel 是顶栏显示文案。
func (a *App) settingsDirtyLabel() string {
	if !a.settingsDirty() {
		return ""
	}
	return strings.TrimSpace("● 未保存修改")
}
