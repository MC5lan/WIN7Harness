//go:build windows

package ui

import "testing"

// TestAppIconFlattensOnBackground 保证内嵌 ico 能解出 PNG 大图，压底后
// 圆角之外的透明区变成底色（不会露出黑角），且同尺寸同底色命中缓存。
func TestAppIconFlattensOnBackground(t *testing.T) {
	if src := decodeIcoLargestPNG(iconBytes); src == nil || src.Bounds().Dx() < 128 {
		t.Fatal("icon.ico 应含可解码的大尺寸 PNG 帧")
	}
	bg := uint32(0x001B1D1F)
	d := AppIcon(96, bg)
	if d == nil || d.W() != 96 || d.H() != 96 {
		t.Fatalf("AppIcon(96) 尺寸不对: %v", d)
	}
	want := []byte{byte(bg >> 16), byte(bg >> 8), byte(bg), 0xFF}
	for i, v := range want {
		if diff := int(d.bits[i]) - int(v); diff < -3 || diff > 3 {
			t.Fatalf("左上角第 %d 字节 = %d，应贴近底色 %d", i, d.bits[i], v)
		}
	}
	if AppIcon(96, bg) != d {
		t.Fatal("同尺寸同底色应命中缓存")
	}
}
