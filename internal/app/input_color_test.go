package app

import "testing"

func TestReadableOnFallsBackToBlackOrWhite(t *testing.T) {
	if got := readableOn(0x00FFFFFF, 0x00FFFFFF, 4.5); got != 0x000000 {
		t.Fatalf("白底白字应退回黑色，实际 %#x", got)
	}
	if got := readableOn(0x000000, 0x000000, 4.5); got != 0x00FFFFFF {
		t.Fatalf("黑底黑字应退回白色，实际 %#x", got)
	}
	if got := readableOn(0x002A2520, 0x00FFFFFF, 4.5); got != 0x002A2520 {
		t.Fatalf("对比度足够时应保留原色，实际 %#x", got)
	}
}

func TestReadableOnRejectsSameLightnessDivergence(t *testing.T) {
	// 浅底浅字：对比度公式在某些令牌组合下会误判为可读，但屏幕上是白底白字。
	if got := readableOn(0x00F5F0EB, 0x00FFFFFF, 4.5); got != 0x000000 {
		t.Fatalf("浅底浅字应退回黑色，实际 %#x", got)
	}
	// 深底深字同理。
	if got := readableOn(0x00292C2E, 0x001B1D1F, 4.5); got != 0x00FFFFFF {
		t.Fatalf("深底深字应退回白色，实际 %#x", got)
	}
}

// 高对比主题的 AccentBase 是亮黄（COLORREF 0x0000D4FF），白字对比度只有 1.4:1。
// 主按钮必须退回黑字。
func TestReadableOnHighContrastAccent(t *testing.T) {
	if got := readableOn(0x00FFFFFF, 0x0000D4FF, 4.5); got != 0x000000 {
		t.Fatalf("高对比亮黄底应使用黑字，实际 %#x", got)
	}
}
