package core

import "testing"

func TestAddPromptHistory(t *testing.T) {
	var h []string
	h = AddPromptHistory(h, "第一条")
	h = AddPromptHistory(h, "第二条")
	h = AddPromptHistory(h, "第二条")   // 相邻重复
	h = AddPromptHistory(h, "/help") // 斜杠命令不记
	h = AddPromptHistory(h, "第一条")   // 全表去重，挪到末尾
	h = AddPromptHistory(h, "  ")    // 空白不记
	if len(h) != 2 || h[0] != "第二条" || h[1] != "第一条" {
		t.Fatalf("unexpected history: %#v", h)
	}
}
