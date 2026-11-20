package ui

// 半透明遮罩的回归测试。
//
// 为什么要单独钉：遮罩横跨"光栅器 → 预乘合成 → 裁剪段合成"三段，
// 任何一段把 alpha 乘两次，表现都不是报错而是"遮罩几乎看不见"或
// "边缘发黑"，肉眼在浅色主题下很容易误判成配色问题。

import (
	"image"
	"image/color"
	"testing"
)

// TestBlitClipPremultiplied 裁剪段合成必须按预乘公式：out = src + dst*(1-a)。
// src 是光栅器输出（颜色已乘过 alpha），再乘一次会让 50% 黑的遮罩只剩 25%。
func TestBlitClipPremultiplied(t *testing.T) {
	dst := image.NewRGBA(image.Rect(0, 0, 1, 1))
	dst.SetRGBA(0, 0, color.RGBA{255, 255, 255, 255})
	// 预乘后的 50% 黑：RGB=0，A=128
	sub := image.NewRGBA(image.Rect(0, 0, 1, 1))
	sub.SetRGBA(0, 0, color.RGBA{0, 0, 0, 128})

	blitClip(dst, image.Point{}, sub, image.Point{}, image.Rect(0, 0, 1, 1))
	got := dst.RGBAAt(0, 0)
	if got.A != 255 {
		t.Fatalf("合成后必须不透明，得到 A=%d", got.A)
	}
	// 期望 255*(1-128/255) ≈ 127，允许 ±3 的取整误差
	if got.R < 124 || got.R > 130 {
		t.Fatalf("50%% 黑遮罩叠白底应得 ~127，得到 %d（说明 alpha 被乘了两次）", got.R)
	}
}

// TestRenderRegionAlphaOverBackground 遮罩叠在同帧先画的底板上，结果必须不透明。
// 脏区重放时整条命令流都会重画：底板(不透明) → 遮罩(半透明)，
// 合成后的像素 alpha 必须是 255，否则这个像素会"继承"上一帧的颜色，越叠越黑。
func TestRenderRegionAlphaOverBackground(t *testing.T) {
	cmds := []rxCmd{
		{kind: rxFill, x: 0, y: 0, w: 40, h: 40, color: 0x00FFFFFF, clip: clipNone},               // 白底
		{kind: rxFill, x: 10, y: 10, w: 20, h: 20, color: 0x00000000, alpha: 0.5, clip: clipNone}, // 50% 黑遮罩
	}
	img := renderRegion(cmds, image.Rect(0, 0, 40, 40))
	if img == nil {
		t.Fatal("renderRegion 返回 nil")
	}
	inside := img.RGBAAt(15, 15)
	if inside.A != 255 {
		t.Fatalf("遮罩区必须不透明，A=%d", inside.A)
	}
	if inside.R < 120 || inside.R > 135 {
		t.Fatalf("遮罩区应为 ~128 灰，得到 %d", inside.R)
	}
	outside := img.RGBAAt(2, 2)
	if outside.R < 250 {
		t.Fatalf("遮罩外仍是白底，得到 %d", outside.R)
	}
}

// TestRxDirtyDetectsAlphaChange 只改不透明度也算改动，否则切换主题/开关抽屉时
// 脏区算成"没变化"，遮罩会整个不刷新。
func TestRxDirtyDetectsAlphaChange(t *testing.T) {
	mk := func(a float64) *rxScene {
		s := &rxScene{}
		s.add(rxCmd{kind: rxFill, x: 0, y: 0, w: 100, h: 100, color: 0, alpha: a})
		return s
	}
	d := rxDirty(mk(0.32), mk(0.5), 200, 200, true, true)
	if d.Empty() {
		t.Fatal("alpha 变化必须产生非空脏区")
	}
	if same := rxDirty(mk(0.32), mk(0.32), 200, 200, true, true); !same.Empty() {
		t.Fatalf("alpha 未变时脏区应为空，得到 %v", same)
	}
}

// TestRenderRegionAlphaClipped 带裁剪的半透明填充也要走自己合成的路子。
// 抽屉遮罩在真实帧里几乎总是带裁剪（前面有 Clip/PopClip），
// 所以这条路径必须和"无裁剪"给出同样的结果。
func TestRenderRegionAlphaClipped(t *testing.T) {
	full := [4]int32{0, 0, 40, 40}
	cmds := []rxCmd{
		{kind: rxFill, x: 0, y: 0, w: 40, h: 40, color: 0x00FFFFFF, clip: full},
		{kind: rxFill, x: 10, y: 10, w: 20, h: 20, color: 0x00000000, alpha: 0.5, clip: full},
	}
	img := renderRegion(cmds, image.Rect(0, 0, 40, 40))
	if img == nil {
		t.Fatal("renderRegion 返回 nil")
	}
	got := img.RGBAAt(15, 15)
	if got.R < 120 || got.R > 135 {
		t.Fatalf("带裁剪的 50%% 遮罩应得 ~128，得到 %d", got.R)
	}
}

// TestAlphaRespectsOrder 半透明填充不能破坏栈序：后画的不透明块要盖住先画的遮罩。
func TestAlphaRespectsOrder(t *testing.T) {
	cmds := []rxCmd{
		{kind: rxFill, x: 0, y: 0, w: 40, h: 40, color: 0x00FFFFFF, clip: clipNone},
		{kind: rxFill, x: 0, y: 0, w: 40, h: 40, color: 0x00000000, alpha: 0.5, clip: clipNone},
		{kind: rxFill, x: 20, y: 20, w: 10, h: 10, color: 0x000000FF, clip: clipNone}, // 不透明红
	}
	img := renderRegion(cmds, image.Rect(0, 0, 40, 40))
	if got := img.RGBAAt(25, 25); got.R < 250 || got.G > 5 {
		t.Fatalf("后画的不透明红应完全覆盖遮罩，得到 %v", got)
	}
	if got := img.RGBAAt(2, 2); got.R < 120 || got.R > 135 {
		t.Fatalf("远处仍应是遮罩后的灰，得到 %d", got.R)
	}
}

// TestLayerSpans 图层标记要能正确切分，且标记本身不产生脏区。
func TestLayerSpans(t *testing.T) {
	cmds := []rxCmd{
		{kind: rxFill, x: 0, y: 0, w: 10, h: 10},
		{kind: rxText, x: 0, y: 0, text: "底"},
		{kind: rxLayer},
		{kind: rxFill, x: 0, y: 0, w: 10, h: 10},
	}
	spans := layerSpans(cmds)
	if len(spans) != 2 {
		t.Fatalf("应切成 2 层，得到 %d：%v", len(spans), spans)
	}
	if spans[0].A != 0 || spans[0].B != 2 || spans[1].A != 3 || spans[1].B != 4 {
		t.Fatalf("切分下标不对：%v", spans)
	}
	if !spanBounds([]rxCmd{{kind: rxLayer}}).Empty() {
		t.Fatal("图层标记不应占据像素")
	}
	if hasGeometry(cmds[3:4]) != true || hasGeometry(cmds[1:2]) != false {
		t.Fatal("hasGeometry 判定不对")
	}
}
