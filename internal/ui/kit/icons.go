package kit

// 图标（暖纸填充 · 面性图标，与 app/icons.go 同款语义）。
// 全部以 16×16 逻辑网格绘制；单色 currentColor，参数 r 任意尺寸等比缩放。
// 需要圆角胶囊/实心剪影时直接用 Painter 的 Round/Dot/HLine 组合，
// 不引入任何第三方依赖。

import (
	"math"

	"win7harness/internal/ui"
)

type ipt struct{ x, y float32 }

func ipol(cx, cy, r, deg float32) ipt {
	rad := deg * 3.1415927 / 180
	return ipt{cx + r*float32(math.Cos(float64(rad))), cy - r*float32(math.Sin(float64(rad)))}
}

func icirclePts(cx, cy, r float32, n int, startDeg, sweepDeg float32) []ipt {
	pts := make([]ipt, 0, n+1)
	for i := 0; i <= n; i++ {
		a := (startDeg + sweepDeg*float32(i)/float32(n)) * 3.1415927 / 180
		pts = append(pts, ipt{cx + r*float32(math.Cos(float64(a))), cy - r*float32(math.Sin(float64(a)))})
	}
	return pts
}

func icapsulePts(x1, y1, x2, y2, thick float32) []ipt {
	dx, dy := x2-x1, y2-y1
	l := float32(math.Sqrt(float64(dx*dx + dy*dy)))
	if l == 0 {
		return nil
	}
	nx, ny := -dy/l*thick/2, dx/l*thick/2
	return []ipt{
		{x1 + nx, y1 + ny}, {x2 + nx, y2 + ny},
		{x2 - nx, y2 - ny}, {x1 - nx, y1 - ny},
	}
}

// ifillPoly 扫描线 even-odd 填充
func ifillPoly(g Painter, pts []ipt, color uint32) {
	if len(pts) < 3 {
		return
	}
	minY, maxY := pts[0].y, pts[0].y
	for _, p := range pts[1:] {
		if p.y < minY {
			minY = p.y
		}
		if p.y > maxY {
			maxY = p.y
		}
	}
	y0, y1 := int32(minY), int32(maxY)+1
	for y := y0; y <= y1; y++ {
		fy := float32(y) + 0.5
		var xs []float32
		for i := 0; i < len(pts); i++ {
			a, b := pts[i], pts[(i+1)%len(pts)]
			if (a.y <= fy) == (b.y <= fy) {
				continue
			}
			xs = append(xs, a.x+(fy-a.y)*(b.x-a.x)/(b.y-a.y))
		}
		if len(xs) < 2 {
			continue
		}
		for i := 1; i < len(xs); i++ {
			for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
				xs[j], xs[j-1] = xs[j-1], xs[j]
			}
		}
		for i := 0; i+1 < len(xs); i += 2 {
			x0, x1 := xs[i], xs[i+1]
			if x1-x0 >= 0.5 {
				g.HLine(int32(x0+0.5), y, int32(x1-x0+0.5), color)
			}
		}
	}
}

func istick(g Painter, pts []ipt, closed bool, sw int32, color uint32) {
	n := len(pts)
	if n < 2 {
		return
	}
	for i := 0; i+1 < n; i++ {
		g.LineW(int32(pts[i].x), int32(pts[i].y), int32(pts[i+1].x), int32(pts[i+1].y), color, sw)
	}
	if closed {
		g.LineW(int32(pts[n-1].x), int32(pts[n-1].y), int32(pts[0].x), int32(pts[0].y), color, sw)
	}
}

func iRoundedRect(x, y, w, h, rad float32, n int) []ipt {
	if rad < 0 {
		rad = 0
	}
	pts := make([]ipt, 0, 4*n+4)
	corners := [][3]float32{{x + w - rad, y + rad, 270}, {x + w - rad, y + h - rad, 0}, {x + rad, y + h - rad, 90}, {x + rad, y + rad, 180}}
	for _, cnr := range corners {
		cx, cy, start := cnr[0], cnr[1], cnr[2]
		for i := 0; i <= n; i++ {
			a := (start + 90*float32(i)/float32(n)) * 3.1415927 / 180
			pts = append(pts, ipt{cx + rad*float32(math.Cos(float64(a))), cy - rad*float32(math.Sin(float64(a)))})
		}
	}
	return pts
}

// drawIcon 在 r 内居中等比绘制命名图标（暖纸填充面性风格）。
func drawIcon(g Painter, kind string, r Rect, color uint32) {
	cx, cy := r.CX(), r.CY()
	u := float32(r.H) / 16.0
	px := func(v float32) float32 { return float32(cx) + v*u }
	py := func(v float32) float32 { return float32(cy) + v*u }
	ix := func(v float32) int32 { return int32(px(v) + 0.5) }
	iy := func(v float32) int32 { return int32(py(v) + 0.5) }
	ln := func(x1, y1, x2, y2 float32, sw int32) {
		g.LineW(ix(x1), iy(y1), ix(x2), iy(y2), color, sw)
	}
	rsz := func(x, y, w, h int32, rad int32) { g.Round(ui.R(x, y, w, h), rad, color) }

	switch kind {
	case "plus": // 新建：粗十字
		rsz(ix(-1.8), iy(-5.4), int32(px(1.8)-px(-1.8)), int32(py(5.4)-py(-5.4)), int32(1.7*u))
		rsz(ix(-5.4), iy(-1.8), int32(px(5.4)-px(-5.4)), int32(py(1.8)-py(-1.8)), int32(1.7*u))

	case "search": // 放大镜：粗环 + 粗柄
		for i := 0; i < 16; i++ { // 粗圆环（两笔 LineW 构一个实心圆环）
			a1 := float32(i) * 22.5
			a2 := float32(i+1) * 22.5
			c1, s1 := float32(math.Cos(float64(a1*3.1415927/180))), float32(math.Sin(float64(a1*3.1415927/180)))
			c2, s2 := float32(math.Cos(float64(a2*3.1415927/180))), float32(math.Sin(float64(a2*3.1415927/180)))
			g.LineW(int32(px(-1.1)+4.4*u*c1), int32(py(-1.1)-4.4*u*s1), int32(px(-1.1)+4.4*u*c2), int32(py(-1.1)-4.4*u*s2), color, 2)
		}
		ifillPoly(g, icapsulePts(px(2.6), py(2.6), px(6.3), py(6.3), 2.3*u), color)

	case "close": // 叉号：两条粗斜线
		ln(-3.9, -3.9, 3.9, 3.9, 3)
		ln(-3.9, 3.9, 3.9, -3.9, 3)

	case "check": // 对号：实心粗勾
		ifillPoly(g, []ipt{
			{px(-5.5), py(0.5)}, {px(-4.0), py(-1.3)}, {px(-1.6), py(1.6)},
			{px(4.6), py(-4.9)}, {px(5.7), py(-3.4)}, {px(-1.6), py(4.6)},
		}, color)

	case "menu": // 汉堡：三条粗面条
		for _, yy := range []float32{-3.4, 0, 3.4} {
			rsz(ix(-5.2), iy(yy-1.15), int32(px(5.2)-px(-5.2)), int32(py(yy+1.15)-py(yy-1.15)), int32(1.15*u))
		}

	case "gear": // 齿轮：实心齿轮外形 + 中心环
		step := float32(360.0 / 8)
		pts := make([]ipt, 0, 33)
		for i := 0; i < 8; i++ {
			a := float32(i) * step
			pts = append(pts, ipol(px(0), py(0), 5.5*u, a-13))
			pts = append(pts, ipol(px(0), py(0), 5.5*u, a+13))
			pts = append(pts, ipol(px(0), py(0), 3.6*u, a+17))
			pts = append(pts, ipol(px(0), py(0), 3.6*u, a+step-17))
		}
		ifillPoly(g, pts, color)
		for i := 0; i < 12; i++ { // 中心环（描边）
			a1 := float32(i) * 30
			a2 := float32(i+1) * 30
			c1, s1 := float32(math.Cos(float64(a1*3.1415927/180))), float32(math.Sin(float64(a1*3.1415927/180)))
			c2, s2 := float32(math.Cos(float64(a2*3.1415927/180))), float32(math.Sin(float64(a2*3.1415927/180)))
			g.LineW(int32(px(0)+2.05*u*c1), int32(py(0)-2.05*u*s1), int32(px(0)+2.05*u*c2), int32(py(0)-2.05*u*s2), color, 2)
		}
	}
}
