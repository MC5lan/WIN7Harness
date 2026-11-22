//go:build windows

package app

import (
	"testing"

	"win7harness/internal/core"
)

func TestSettingsFieldValidation(t *testing.T) {
	a := &App{ui: NewUI(nil, nil), cfg: core.DefaultConfig()}
	a.ui.SetField("set.maxtok", "abc")
	if id, _ := a.validateSettingsFields(); id != "set.maxtok" {
		t.Fatalf("非法 token 字段未拦截：%q", id)
	}
	a.ui.SetField("set.maxtok", "8192")
	a.ui.SetField("set.ctxwin", "960k")
	if id, msg := a.validateSettingsFields(); id != "" {
		t.Fatalf("合法字段被拦截：%s %s", id, msg)
	}
	a.ui.SetField("set.ctxwin", "32x")
	if id, _ := a.validateSettingsFields(); id != "set.ctxwin" {
		t.Fatalf("非法窗口未拦截：%q", id)
	}
}

func TestSettingsDirtyState(t *testing.T) {
	a := &App{ui: NewUI(nil, nil), cfg: core.DefaultConfig()}
	a.initForms()
	if a.settingsDirty() {
		t.Fatal("初始化后不应是未保存状态")
	}
	a.ui.SetField("set.base", "https://example.com/v1")
	if !a.settingsDirty() {
		t.Fatal("修改字段后应标记为未保存")
	}
	a.settingsMarkSaved()
	if a.settingsDirty() {
		t.Fatal("保存后应清除未保存状态")
	}
}
