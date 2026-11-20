package app

import "testing"

func TestClampChatScrollAtStreamEnd(t *testing.T) {
	// 流式结束时内容高度从 2000 缩到 1200，旧 scroll 停在 1500：
	// 必须夹到 1200-600=600，而不是留一片空白。
	if got := clampChatScroll(1500, 1200, 600, false); got != 600 {
		t.Fatalf("内容变短后应夹到 600，实际 %v", got)
	}
	if got := clampChatScroll(1500, 1200, 600, true); got != 600 {
		t.Fatalf("autoScroll 应贴底 600，实际 %v", got)
	}
	if got := clampChatScroll(-20, 1200, 600, false); got != 0 {
		t.Fatalf("负 scroll 应回到 0，实际 %v", got)
	}
	if got := clampChatScroll(100, 400, 600, true); got != 0 {
		t.Fatalf("内容不足一屏时应为 0，实际 %v", got)
	}
}
