package ui

import (
	"image"
	"testing"
)

// 帧序列驱动脏区：首帧全屏、静止帧为空、尾部变化只覆盖一小块、删除命令必须擦旧像素。
func TestRxDirty(t *testing.T) {
	var cur, prev rxScene
	full := image.Rect(0, 0, 400, 300)

	cur.add(rxCmd{kind: rxFill, x: 0, y: 0, w: 400, h: 300})
	cur.add(rxCmd{kind: rxFill, x: 10, y: 10, w: 20, h: 20})

	if d := rxDirty(&cur, &prev, 400, 300, false, true); d != full {
		t.Fatalf("首帧应为全屏，得到 %v", d)
	}

	prev.cmds = append(prev.cmds[:0], cur.cmds...)
	if d := rxDirty(&cur, &prev, 400, 300, true, true); !d.Empty() {
		t.Fatalf("完全一致的帧脏区应为空，得到 %v", d)
	}

	// 只改最后一条命令的位置：脏区应同时覆盖旧位置与新位置，且远小于全屏。
	prev.cmds = append(prev.cmds[:0], cur.cmds...)
	cur.cmds[1].x = 200
	d := rxDirty(&cur, &prev, 400, 300, true, true)
	if d.Empty() {
		t.Fatal("位置变化必须产生脏区")
	}
	if !d.Overlaps(image.Rect(8, 8, 34, 34)) || !d.Overlaps(image.Rect(196, 8, 226, 34)) {
		t.Fatalf("脏区应同时包含新旧位置，得到 %v", d)
	}
	if d.Dx() > 260 || d.Dy() > 60 {
		t.Fatalf("脏区过大，失去增量意义：%v", d)
	}

	// 删掉最后一条命令：旧像素必须被擦掉，脏区不能为空。
	prev.cmds = append(prev.cmds[:0], cur.cmds...)
	cur.cmds = cur.cmds[:1]
	d = rxDirty(&cur, &prev, 400, 300, true, true)
	if !d.Overlaps(image.Rect(198, 8, 228, 34)) {
		t.Fatalf("删除命令后脏区须覆盖旧区域，得到 %v", d)
	}

	// 脏区不得越出画布。
	cur.cmds = append(cur.cmds[:0], rxCmd{kind: rxFill, x: -50, y: -50, w: 900, h: 900})
	d = rxDirty(&cur, &prev, 400, 300, true, true)
	if d != full {
		t.Fatalf("越界命令的脏区应被裁到画布内，得到 %v", d)
	}
}

// 半透明几何必须与底层做预乘合成，全透明区域保留底层（脏区外的上一帧内容）。
func TestCompositeOver(t *testing.T) {
	dst := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for i := range dst.Pix {
		dst.Pix[i] = 0x30
	}
	dst.Pix[3] = 0xFF
	dst.Pix[7] = 0xFF

	sub := image.NewRGBA(image.Rect(0, 0, 2, 1))
	// 第 0 像素：50% 黑（预乘后 RGB=0,a=128）→ 底层应减半。
	sub.Pix[0], sub.Pix[1], sub.Pix[2], sub.Pix[3] = 0, 0, 0, 128
	// 第 1 像素：全透明 → 底层保持不变。
	sub.Pix[4], sub.Pix[5], sub.Pix[6], sub.Pix[7] = 0, 0, 0, 0

	compositeOver(dst, image.Point{X: 0, Y: 0}, sub)

	if got := dst.Pix[0]; got == 0x30 || got > 0x1A {
		t.Fatalf("50%% 黑应把底层压到约 0x17，得到 0x%02X", got)
	}
	if got := dst.Pix[4]; got != 0x30 {
		t.Fatalf("全透明像素不应改动底层，得到 0x%02X", got)
	}
	if dst.Pix[3] != 0xFF {
		t.Fatal("合成后 alpha 必须为不透明")
	}
}
