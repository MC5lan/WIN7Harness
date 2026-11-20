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
