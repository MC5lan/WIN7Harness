//go:build windows

package ui

import "testing"

func TestQuantizeGDIBoundsAndKeepsColors(t *testing.T) {
	if got := quantizeGDI(0x00000000); got != 0 {
		t.Fatalf("黑色量化后应保持黑色：%#x", got)
	}
	if got := quantizeGDI(0x00FFFFFF); got != 0x00FFFFFF {
		t.Fatalf("白色量化后应保持白色：%#x", got)
	}
	// COLORREF 0x00BBGGRR：R=0x56 -> 0x40，G=0x34 -> 0x20，B=0x12 -> 0x00。
	if got := quantizeGDI(0x00123456); got != 0x00002040 {
		t.Fatalf("量化结果异常：%#x", got)
	}
	// 同一 3 位色阶内的颜色应复用同一个 GDI 对象。
	if quantizeGDI(0x00123456) != quantizeGDI(0x0012345F) {
		t.Fatal("相邻颜色应落到同一色阶")
	}
}
