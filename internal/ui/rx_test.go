package ui

import (
	"image"
	"testing"
)

// TestRxRegionOffset 验证区域重放把绝对坐标平移到区域原点。
func TestRxRegionOffset(t *testing.T) {
	cmds := []rxCmd{
		{kind: rxFill, x: 10, y: 20, w: 100, h: 50, color: 0x0000FF}, // 红
		{kind: rxFill, x: 30, y: 40, w: 20, h: 20, color: 0x00FF00},  // 绿
	}
	region := image.Rect(20, 30, 60, 60)
	img := renderRegion(cmds, region)
	if img == nil {
		t.Fatal("renderRegion 返回 nil")
	}
	// 绿块物理 (30,40)-(50,60) → 区域内 (10,10)-(30,30)
	if c := img.RGBAAt(20, 20); c.R > 30 || c.G < 200 || c.B > 30 {
		t.Fatalf("区域中心应为绿色，实际 %v", c)
	}
	// 红块物理 (10,20)-(110,70) 覆盖区域左上角 (0,0)
	if c := img.RGBAAt(2, 2); c.R < 200 || c.G > 40 {
		t.Fatalf("区域左上应为红色，实际 %v", c)
	}
}

// TestRxRoundClip 验证圆角矩形在裁剪段内只画在裁剪区域内。
func TestRxRoundClip(t *testing.T) {
	clip := [4]int32{40, 10, 120, 60}
	cmds := []rxCmd{
		{kind: rxRound, x: 0, y: 0, w: 200, h: 100, r: 12, color: 0x00FF00, clip: clip},
	}
	region := image.Rect(0, 0, 200, 100)
	img := renderRegion(cmds, region)
	// 裁剪区内
	if c := img.RGBAAt(80, 40); c.G < 200 {
		t.Fatalf("裁剪区内应为绿色，实际 %v", c)
	}
	// 裁剪区外（左侧）
	if c := img.RGBAAt(20, 40); c.A != 0 && c.G > 40 {
		t.Fatalf("裁剪区外不应有绿色，实际 %v", c)
	}
	// 裁剪区外（下侧，卡片圆角本来覆盖这里）
	if c := img.RGBAAt(80, 80); c.A != 0 && c.G > 40 {
		t.Fatalf("裁剪区下侧不应有绿色，实际 %v", c)
	}
	// 圆角处（卡片左上角外侧）应无填充
	cmds2 := []rxCmd{{kind: rxRound, x: 0, y: 0, w: 100, h: 100, r: 20, color: 0x00FF00}}
	img2 := renderRegion(cmds2, image.Rect(0, 0, 100, 100))
	if c := img2.RGBAAt(1, 1); c.A > 20 && c.G > 60 {
		t.Fatalf("圆角外侧不应为实色，实际 %v", c)
	}
	if c := img2.RGBAAt(50, 50); c.G < 200 {
		t.Fatalf("圆心类区域应为填充色，实际 %v", c)
	}
}

// TestRxGradient 验证渐变方向与端点颜色。
func TestRxGradient(t *testing.T) {
	cmds := []rxCmd{{kind: rxGradient, x: 0, y: 0, w: 40, h: 100, color: 0x000000, grad: 0xFFFFFF, vert: true}}
	img := renderRegion(cmds, image.Rect(0, 0, 40, 100))
	top := img.RGBAAt(20, 2)
	bot := img.RGBAAt(20, 97)
	if top.R > 40 || top.G > 40 || top.B > 40 {
		t.Fatalf("渐变顶部应接近起始色(黑)，实际 %v", top)
	}
	if bot.R < 200 || bot.G < 200 || bot.B < 200 {
		t.Fatalf("渐变底部应接近终止色(白)，实际 %v", bot)
	}
}

// TestRxStroke 验证描边落在矩形范围内且中心不填充。
func TestRxStroke(t *testing.T) {
	cmds := []rxCmd{{kind: rxRoundStroke, x: 0, y: 0, w: 40, h: 40, r: 6, sw: 1, color: 0x00FF00}}
	img := renderRegion(cmds, image.Rect(0, 0, 40, 40))
	if c := img.RGBAAt(20, 20); c.G > 60 && c.A > 60 {
		t.Fatalf("描边中心不应填充，实际 %v", c)
	}
	if c := img.RGBAAt(0, 20); c.G < 100 {
		t.Fatalf("左边界应为描边色，实际 %v", c)
	}
	if c := img.RGBAAt(45, 45); c.A != 0 {
		t.Fatalf("矩形外不应有像素，实际 %v", c)
	}
}

// TestRxSceneDiff 验证帧比对与脏区并集。
func TestRxSceneDiff(t *testing.T) {
	a := &rxScene{}
	a.add(rxCmd{kind: rxFill, x: 0, y: 0, w: 10, h: 10, color: 0xFF})
	a.add(rxCmd{kind: rxText, x: 0, y: 0, text: "hello", size: 12})
	b := &rxScene{}
	b.add(rxCmd{kind: rxFill, x: 0, y: 0, w: 10, h: 10, color: 0xFF})
	b.add(rxCmd{kind: rxText, x: 0, y: 0, text: "hello", size: 12})
	if a.firstDiff(b) != -1 {
		t.Fatalf("相同帧应返回 -1")
	}
	b.add(rxCmd{kind: rxFill, x: 0, y: 0, w: 10, h: 10, color: 0xEE})
	if got := a.firstDiff(b); got != 2 {
		t.Fatalf("差异下标应为 2，实际 %d", got)
	}
	r := b.unionFrom(2)
	if r.Empty() {
		t.Fatal("脏区不应为空")
	}
}
