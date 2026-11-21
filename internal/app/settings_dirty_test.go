//go:build windows

package app

import (
	"testing"

	"win7harness/internal/core"
)

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
