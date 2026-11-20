package ui

import (
	"image"
	"image/png"
	"os"
	"testing"
	"time"
)

// buildPanel 构造一帧仿真面板：底色 + 顶栏 + 左侧栏 + 内容卡片 + 一条悬停高亮。
func buildPanel(s *rxScene, hover int, W, H float64) {
	s.reset()
	s.add(rxCmd{kind: rxFill, x: 0, y: 0, w: W, h: H, color: 0x1A1614})
	s.add(rxCmd{kind: rxGradient, x: 0, y: 0, w: W, h: 56, color: 0x2A2320, grad: 0x1A1614, vert: true})
	s.add(rxCmd{kind: rxLine, x: 0, y: 55.5, x2: W, y2: 55.5, sw: 1, color: 0x3A322C})
	s.add(rxCmd{kind: rxFill, x: 0, y: 56, w: 208, h: H - 56, color: 0x201B18})
	s.add(rxCmd{kind: rxLine, x: 207.5, y: 56, x2: 207.5, y2: H, sw: 1, color: 0x3A322C})
	for i := 0; i < 6; i++ {
		y := 76 + float64(i)*40
		if i == hover {
			s.add(rxCmd{kind: rxRound, x: 12, y: y - 6, w: 184, h: 34, r: 8, color: 0x33291F})
		}
		s.add(rxCmd{kind: rxDot, x: 30, y: y + 9, r: 4, color: 0xE8A33D})
	}
	for i := 0; i < 4; i++ {
		y := 76 + float64(i)*96
		s.add(rxCmd{kind: rxRound, x: 228, y: y, w: W - 252, h: 84, r: 10, color: 0x251F1B})
		s.add(rxCmd{kind: rxRoundStroke, x: 228, y: y, w: W - 252, h: 84, r: 10, sw: 1, color: 0x3A322C})
		s.add(rxCmd{kind: rxRing, x: 246, y: y + 20, r: 6, sw: 2, color: 0xE8A33D})
	}
	s.add(rxCmd{kind: rxRound, x: W - 300, y: H - 60, w: 280, h: 40, r: 20, color: 0xE8A33D})
}

// 帧序列的端到端验证：脏区面积、光栅耗时，同时导出第 3 帧的几何合成图供人眼核对。
func TestFrameSequence(t *testing.T) {
	const W, H = 1280, 800
	var cur, prev rxScene
	frame := image.NewRGBA(image.Rect(0, 0, W, H))
	out := os.Getenv("RX_FRAME")

	run := func(name string, hover int, hasPrev bool) time.Duration {
		buildPanel(&cur, hover, W, H)
		d := rxDirty(&cur, &prev, W, H, hasPrev, true)
		start := time.Now()
		if !d.Empty() {
			if sub := renderRegion(cur.cmds, d); sub != nil {
				compositeOver(frame, d.Min, sub)
			}
		}
		el := time.Since(start)
		area := float64(d.Dx()*d.Dy()) / float64(W*H) * 100
		t.Logf("%-10s 脏区 %4dx%-4d 占屏 %5.1f%%  光栅耗时 %v", name, d.Dx(), d.Dy(), area, el)
		prev.cmds = append(prev.cmds[:0], cur.cmds...)
		return el
	}

	full := run("首帧", 0, false)
	still := run("静止帧", 0, true)
	hover := run("悬停变化", 3, true)

	if full <= 0 {
		t.Fatal("首帧必须产生耗时")
	}
	if still > full/10 {
		t.Fatalf("静止帧应几乎零成本：%v vs 首帧 %v", still, full)
	}
	if hover > full/2 {
		t.Fatalf("单点悬停变化应远低于全量：%v vs %v", hover, full)
	}
	t.Logf("加速比：全量/悬停 = %.1fx", float64(full)/float64(hover+1))

	if out != "" {
		os.MkdirAll(out[:len(out)-len("/frame3.png")], 0o755)
		f, err := os.Create(out)
		if err == nil {
			png.Encode(f, frame)
			f.Close()
			t.Logf("帧合成图已写入 %s", out)
		}
	}
}
