package ui

import (
	"image"
	"image/png"
	"os"
	"testing"
)

// TestRenderSample 生成一张仿真面板样张，用于人工核对视觉效果（仅在有环境变量时落盘）。
func TestRenderSample(t *testing.T) {
	out := os.Getenv("RX_SAMPLE")
	if out == "" {
		t.Skip("未设置 RX_SAMPLE")
	}
	W, H := 960, 600
	var cmds []rxCmd

	bg := uint32(0x1B1410) // COLORREF: R=0x10 G=0x14 B=0x1B
	cmds = append(cmds, rxCmd{kind: rxGradient, x: 0, y: 0, w: float64(W), h: float64(H),
		color: bg, grad: 0x2A1E16, vert: true})

	// 顶栏
	cmds = append(cmds, rxCmd{kind: rxFill, x: 0, y: 0, w: float64(W), h: 52, color: 0x241B14})
	cmds = append(cmds, rxCmd{kind: rxLine, x: 0, y: 52, x2: float64(W), y2: 52, sw: 1, color: 0x3A2A1C})

	// 左侧主卡片：圆角 + 发光边
	cmds = append(cmds, rxCmd{kind: rxRound, x: 24, y: 76, w: 560, h: 300, r: 14, color: 0x2A2018})
	cmds = append(cmds, rxCmd{kind: rxRoundStroke, x: 24, y: 76, w: 560, h: 300, r: 14, sw: 1, color: 0x6B4A22})
	cmds = append(cmds, rxCmd{kind: rxRoundStroke, x: 24, y: 76, w: 560, h: 300, r: 14, sw: 4, color: 0x3A2A14})

	// 右侧小卡片
	cmds = append(cmds, rxCmd{kind: rxRound, x: 608, y: 76, w: 328, h: 140, r: 12, color: 0x2A2018})
	cmds = append(cmds, rxCmd{kind: rxRoundStroke, x: 608, y: 76, w: 328, h: 140, r: 12, sw: 1, color: 0x6B4A22})

	// 状态圆点 + 圆环
	cmds = append(cmds, rxCmd{kind: rxDot, x: 636, y: 106, r: 5, color: 0x22DD88})
	cmds = append(cmds, rxCmd{kind: rxRing, x: 636, y: 106, r: 11, sw: 1.5, color: 0x1E7A50})

	// 进度条：底 + 渐变填充
	cmds = append(cmds, rxCmd{kind: rxRound, x: 636, y: 150, w: 272, h: 10, r: 5, color: 0x1A140E})
	cmds = append(cmds, rxCmd{kind: rxRound, x: 636, y: 150, w: 172, h: 10, r: 5, color: 0x00C2FF})

	// 底部输入框
	cmds = append(cmds, rxCmd{kind: rxRound, x: 24, y: 520, w: 912, h: 56, r: 12, color: 0x241B14})
	cmds = append(cmds, rxCmd{kind: rxRoundStroke, x: 24, y: 520, w: 912, h: 56, r: 12, sw: 1, color: 0x6B4A22})

	// 裁剪区：模拟滚动列表，卡片越界部分应被裁掉
	clip := [4]int32{24, 400, 584, 500}
	cmds = append(cmds, rxCmd{kind: rxRound, x: 24, y: 380, w: 560, h: 100, r: 12, color: 0x3A2A1C, clip: clip})
	cmds = append(cmds, rxCmd{kind: rxFill, x: 24, y: 470, w: 560, h: 40, color: 0x552211, clip: clip})

	img := renderRegion(cmds, image.Rect(0, 0, W, H))
	if img == nil {
		t.Fatal("样张渲染失败")
	}
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	t.Logf("样张已写入 %s", out)
}
