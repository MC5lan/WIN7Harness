package core

import "testing"

func TestMatchBinding(t *testing.T) {
	def := DefaultKeyBindings()
	if !MatchBinding("historySearch", def, 'R', true, false, false) {
		t.Fatal("默认 ctrl+r 应匹配 historySearch")
	}
	if MatchBinding("historySearch", def, 'R', false, false, false) {
		t.Fatal("少了 Ctrl 不应匹配")
	}
	if MatchBinding("historySearch", def, 'R', true, true, false) {
		t.Fatal("多了 Alt 不应匹配")
	}
	custom := map[string]string{"historySearch": "ctrl+shift+h"}
	if !MatchBinding("historySearch", custom, 'H', true, false, true) {
		t.Fatal("自定义 ctrl+shift+h 应生效")
	}
	if MatchBinding("historySearch", custom, 'R', true, false, false) {
		t.Fatal("自定义后旧组合键应失效")
	}
	// 配置缺项时用默认值兜底，不能导致快捷键整体失效。
	if !MatchBinding("palette", map[string]string{}, 'K', true, false, false) {
		t.Fatal("空配置应回退到默认 ctrl+k")
	}
}

func TestParseCombo(t *testing.T) {
	c, a, s, vk, ok := parseCombo("Ctrl+Shift+F12")
	if !ok || !c || !s || a || vk != 0x7B {
		t.Errorf("parseCombo(Ctrl+Shift+F12) = %v %v %v %d %v", c, a, s, vk, ok)
	}
	if _, _, _, _, ok := parseCombo("ctrl+???"); ok {
		t.Error("非法组合键应返回 ok=false")
	}
}
