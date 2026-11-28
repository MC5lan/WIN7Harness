//go:build windows

package ui

import (
	"image"
	"testing"
)

// 图片缓存按字节预算淘汰，而不是只按 64 条；否则 64 张大图能占上 GB。
func TestImageCacheByteBudget(t *testing.T) {
	ic := NewImageCache()
	ic.m["old"] = &DibImage{}
	ic.bytesUsed = maxImageCacheBytes

	d := ic.Get("new", func() (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
	})
	if d == nil {
		t.Fatal("加载失败")
	}
	if _, ok := ic.m["old"]; ok {
		t.Fatal("超过字节预算后应清空旧缓存")
	}
	if _, ok := ic.m["new"]; !ok {
		t.Fatal("新图片应进入缓存")
	}
	if ic.bytesUsed <= 0 || ic.bytesUsed > maxImageCacheBytes {
		t.Fatalf("字节计数异常：%d", ic.bytesUsed)
	}
}

func TestImageCacheClearResetsBytes(t *testing.T) {
	ic := NewImageCache()
	ic.Get("a", func() (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
	})
	ic.Clear()
	if ic.bytesUsed != 0 || len(ic.m) != 0 {
		t.Fatalf("Clear 未重置：bytes=%d len=%d", ic.bytesUsed, len(ic.m))
	}
}
