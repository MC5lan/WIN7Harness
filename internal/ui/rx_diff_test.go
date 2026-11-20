package ui

import (
	"image"
	"testing"
)

// cmdAt 造一条位于 (x,y) 的小方块命令，用来观察脏区落在哪儿。
func cmdAt(x, y float64) rxCmd {
	return rxCmd{kind: rxFill, x: x, y: y, w: 4, h: 4, color: 0x808080}
}

func cmdsAt(coords ...[2]float64) []rxCmd {
	out := make([]rxCmd, 0, len(coords))
	for _, c := range coords {
		out = append(out, cmdAt(c[0], c[1]))
	}
	return out
}

// TestDiffBoundsInsertAtHead 在命令流开头插入一条（左侧栏 hover 的典型形态）。
// 旧实现会从这条一路算到末尾；新实现应该只标记插入的那条。
func TestDiffBoundsInsertAtHead(t *testing.T) {
	prev := cmdsAt([2]float64{100, 100}, [2]float64{200, 200}, [2]float64{300, 300})
	cur := append([]rxCmd{cmdAt(10, 10)}, prev...)

	got := rxDiffBounds(cur, prev, image.Rect(0, 0, 1000, 800))
	if got.Empty() {
		t.Fatal("插入了命令却没算出脏区")
	}
	if got.Dx() > 20 || got.Dy() > 20 {
		t.Fatalf("脏区应只覆盖插入的那条（约 4x4），实际 %v", got)
	}
	if !got.Overlaps(image.Rect(8, 8, 16, 16)) {
		t.Fatalf("脏区没盖住插入点：%v", got)
	}
}

// TestDiffBoundsDeleteAtHead 删除开头的命令：脏区要盖住被删掉的旧位置，
// 否则那块像素会留在屏幕上（表现为"按钮残影"）。
func TestDiffBoundsDeleteAtHead(t *testing.T) {
	prev := cmdsAt([2]float64{10, 10}, [2]float64{200, 200}, [2]float64{300, 300})
	cur := prev[1:]

	got := rxDiffBounds(cur, prev, image.Rect(0, 0, 1000, 800))
	if !got.Overlaps(image.Rect(8, 8, 16, 16)) {
		t.Fatalf("脏区没盖住被删除的位置：%v", got)
	}
	if got.Dx() > 20 || got.Dy() > 20 {
		t.Fatalf("删一条不该波及远处，实际 %v", got)
	}
}

// TestDiffBoundsInsertMiddle 中间插入，前后都要能重新对齐。
func TestDiffBoundsInsertMiddle(t *testing.T) {
	prev := cmdsAt([2]float64{100, 100}, [2]float64{200, 200}, [2]float64{300, 300}, [2]float64{400, 400})
	cur := append([]rxCmd{}, prev[:2]...)
	cur = append(cur, cmdAt(250, 250))
	cur = append(cur, prev[2:]...)

	got := rxDiffBounds(cur, prev, image.Rect(0, 0, 1000, 800))
	if !got.Overlaps(image.Rect(248, 248, 256, 256)) {
		t.Fatalf("脏区没盖住插入点：%v", got)
	}
	if got.Max.X > 320 && got.Max.Y > 320 {
		t.Fatalf("中间插入不该把后半段也算脏：%v", got)
	}
}

// TestDiffBoundsAllChanged 全变时必须覆盖到每个位置，不能漏。
func TestDiffBoundsAllChanged(t *testing.T) {
	prev := cmdsAt([2]float64{10, 10}, [2]float64{20, 20})
	cur := cmdsAt([2]float64{500, 500}, [2]float64{700, 700}, [2]float64{900, 760})

	got := rxDiffBounds(cur, prev, image.Rect(0, 0, 1000, 800))
	for _, c := range [][2]float64{{10, 10}, {20, 20}, {500, 500}, {700, 700}, {900, 760}} {
		r := image.Rect(int(c[0]), int(c[1]), int(c[0])+4, int(c[1])+4)
		if !got.Overlaps(r) {
			t.Fatalf("漏标了 %v：脏区 %v", c, got)
		}
	}
}

// TestDiffBoundsSkipLimit 差异超过跳跃上限时要退化成"逐条标记"，不能漏。
func TestDiffBoundsSkipLimit(t *testing.T) {
	prev := cmdsAt([2]float64{100, 100})
	var cur []rxCmd
	for i := 0; i < 20; i++ {
		cur = append(cur, cmdAt(float64(10+i*5), 10))
	}
	cur = append(cur, prev...)

	got := rxDiffBounds(cur, prev, image.Rect(0, 0, 1000, 800))
	for i := 0; i < 20; i++ {
		x := float64(10 + i*5)
		r := image.Rect(int(x), 10, int(x)+4, 14)
		if !got.Overlaps(r) {
			t.Fatalf("漏标了第 %d 条：%v", i, got)
		}
	}
}

// TestDiffBoundsEqual 完全一致时脏区必须为空，否则每帧都白画。
func TestDiffBoundsEqual(t *testing.T) {
	a := cmdsAt([2]float64{1, 1}, [2]float64{2, 2})
	b := cmdsAt([2]float64{1, 1}, [2]float64{2, 2})
	if got := rxDiffBounds(a, b, image.Rect(0, 0, 100, 100)); !got.Empty() {
		t.Fatalf("命令流一致却算出脏区：%v", got)
	}
}
