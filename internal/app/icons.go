package app

import (
	"math"

	"win7harness/internal/ui"
)

// 图标一律用线段/圆/矩形现画，不依赖字体里的几何符号。
// 暖纸填充·面性图标：实心剪影为主、粗描边为辅，小尺寸下对比更强。
// 所有图标在 16×16 逻辑网格上设计（u = r.H/16），超出网格的部分一律裁掉。
//
// 设计纪律：
//   - 单色 `color` 语义（调用方传入 currentColor：常态 TextDim、激活 Accent 等）
//   - 剪影类（bell/bolt/pencil/...）走 fillPoly 扫描线 even-odd 填充；
//     需要镂空时把孔形画成同色多边形，even-odd 自动打洞
//   - 描边类（clock/image/coin/...）统一 sw = 2 或 3，端点感由 LineW 保证
//   - 不引入第三方图标库

// iconKind 把 "ico:name" 形式的按钮文案拆成图标名。
func iconKind(glyph string) (string, bool) {
	const p = "ico:"
	if len(glyph) > len(p) && glyph[:len(p)] == p {
		return glyph[len(p):], true
	}
	return "", false
}

// IconSize 图标基准边长（逻辑像素）。
const IconSize = 16

type pt struct{ x, y float32 }

func pol(cx, cy, r, deg float32) pt {
	rad := deg * 3.1415927 / 180
	return pt{cx + r*cosf(rad), cy - r*sinf(rad)}
}

// circlePts 生成圆弧/圆的点列（cx,cy 圆心，r 半径，start/sweep 角度制）。
func circlePts(cx, cy, r float32, n int, startDeg, sweepDeg float32) []pt {
	pts := make([]pt, 0, n+1)
	for i := 0; i <= n; i++ {
		a := (startDeg + sweepDeg*float32(i)/float32(n)) * 3.1415927 / 180
		pts = append(pts, pt{cx + r*cosf(a), cy - r*sinf(a)})
	}
	return pts
}

// capsulePts 对角线胶囊（两点之间粗细为 thick 的粗线段）的外轮廓点列。
func capsulePts(x1, y1, x2, y2, thick float32) []pt {
	dx, dy := x2-x1, y2-y1
	l := sqrtf(dx*dx + dy*dy)
	if l == 0 {
		return nil
	}
	nx, ny := -dy/l*thick/2, dx/l*thick/2
	return []pt{
		{x1 + nx, y1 + ny}, {x2 + nx, y2 + ny},
		{x2 - nx, y2 - ny}, {x1 - nx, y1 - ny},
	}
}

// fillPoly 扫描线 even-odd 填充（凸/凹都可，孔形用自相交/反向缠绕产生）。
func fillPoly(c *ui.Canvas, pts []pt, color uint32) {
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
		for i := 1; i < len(xs); i++ { // 插入排序（交点数很少）
			for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
				xs[j], xs[j-1] = xs[j-1], xs[j]
			}
		}
		for i := 0; i+1 < len(xs); i += 2 {
			x0, x1 := xs[i], xs[i+1]
			if x1-x0 >= 0.5 {
				c.HLine(int32(x0+0.5), y, int32(x1-x0+0.5), color)
			}
		}
	}
}

// strokePoly 折线描边（每段 LineW；闭合时首尾相连）。
func strokePoly(c *ui.Canvas, pts []pt, closed bool, sw int32, color uint32) {
	n := len(pts)
	if n < 2 {
		return
	}
	for i := 0; i+1 < n; i++ {
		c.LineW(int32(pts[i].x), int32(pts[i].y), int32(pts[i+1].x), int32(pts[i+1].y), color, sw)
	}
	if closed {
		c.LineW(int32(pts[n-1].x), int32(pts[n-1].y), int32(pts[0].x), int32(pts[0].y), color, sw)
	}
}

func roundedRect(x, y, w, h, rad float32, n int) []pt {
	if rad < 0 {
		rad = 0
	}
	pts := make([]pt, 0, 4*n+4)
	corners := [][3]float32{{x + w - rad, y + rad, 270}, {x + w - rad, y + h - rad, 0}, {x + rad, y + h - rad, 90}, {x + rad, y + rad, 180}}
	for _, cnr := range corners {
		cx, cy, start := cnr[0], cnr[1], cnr[2]
		for i := 0; i <= n; i++ {
			a := (start + 90*float32(i)/float32(n)) * 3.1415927 / 180
			pts = append(pts, pt{cx + rad*cosf(a), cy - rad*sinf(a)})
		}
	}
	return pts
}

// drawIcon 在 r 内居中等比绘制命名图标（暖纸填充面性风格）。
func drawIcon(c *ui.Canvas, kind string, r ui.Rect, color uint32) {
	cx, cy := r.CX(), r.CY()
	u := float32(r.H) / float32(IconSize)
	px := func(v float32) float32 { return float32(cx) + v*u }
	py := func(v float32) float32 { return float32(cy) + v*u }
	ix := func(v float32) int32 { return int32(px(v) + 0.5) }
	iy := func(v float32) int32 { return int32(py(v) + 0.5) }
	ln := func(x1, y1, x2, y2 float32, sw int32) {
		c.LineW(ix(x1), iy(y1), ix(x2), iy(y2), color, sw)
	}
	rsz := func(x, y, w, h int32, rad int32) { c.Round(ui.R(x, y, w, h), rad, color) }

	switch kind {
	case "plus": // 新建：粗十字
		rsz(ix(-1.8), iy(-5.4), int32(px(1.8)-px(-1.8)), int32(py(5.4)-py(-5.4)), int32(1.7*u))
		rsz(ix(-5.4), iy(-1.8), int32(px(5.4)-px(-5.4)), int32(py(1.8)-py(-1.8)), int32(1.7*u))

	case "search": // 放大镜：粗环 + 粗柄
		strokePoly(c, circlePts(px(-1.1), py(-1.1), 4.5*u, 14, 0, 360), true, 2, color)
		fillPoly(c, capsulePts(px(2.6), py(2.6), px(6.3), py(6.3), 2.3*u), color)

	case "gear": // 齿轮：实心齿轮外形 + 中心环
		step := float32(360.0 / 8)
		pts := make([]pt, 0, 33)
		for i := 0; i < 8; i++ {
			a := float32(i) * step
			pts = append(pts, pol(px(0), py(0), 5.5*u, a-13))
			pts = append(pts, pol(px(0), py(0), 5.5*u, a+13))
			pts = append(pts, pol(px(0), py(0), 3.6*u, a+17))
			pts = append(pts, pol(px(0), py(0), 3.6*u, a+step-17))
		}
		fillPoly(c, pts, color)
		strokePoly(c, circlePts(px(0), py(0), 2.05*u, 10, 0, 360), true, 2, color)

	case "theme": // 明暗对比：半黑半白圆
		strokePoly(c, circlePts(px(0), py(0), 4.8*u, 14, 0, 360), true, 2, color)
		half := circlePts(px(0), py(0), 3.0*u, 10, -90, 180)
		fillPoly(c, append(half, pt{px(0), py(-3.0)}), color)

	case "panel": // 右侧栏：外框 + 分区竖线
		strokePoly(c, roundedRect(px(-5.6), py(-4.6), 11.2*u, 9.2*u, 1.8*u, 3), true, 2, color)
		ln(1.9, -4.4, 1.9, 4.4, 2)

	case "menu": // 汉堡：三条粗面条
		for _, yy := range []float32{-3.4, 0, 3.4} {
			rsz(ix(-5.2), iy(yy-1.15), int32(px(5.2)-px(-5.2)), int32(py(yy+1.15)-py(yy-1.15)), int32(1.15*u))
		}

	case "inbox": // 收件箱：开口托盘
		strokePoly(c, []pt{
			{px(-5.8), py(1.0)}, {px(-2.6), py(1.0)}, {px(-1.2), py(3.2)}, {px(1.2), py(3.2)},
			{px(2.6), py(1.0)}, {px(5.8), py(1.0)}, {px(5.8), py(4.6)}, {px(-5.8), py(4.6)},
		}, true, 2, color)

	case "chart": // 柱状图：三根高低不同的粗柱
		bars := [][2]float32{{-4.5, 0.2}, {-0.9, -2.6}, {2.7, -5.2}}
		for _, b := range bars {
			x0, top := b[0], b[1]
			rsz(ix(x0), iy(top), int32(px(x0+2.3)-px(x0)), int32(py(4.9)-py(top)), int32(1.15*u))
		}

	case "grid": // 四宫格：四个圆角方块
		for _, cellPt := range [][2]float32{{-4.9, -4.9}, {1.7, -4.9}, {-4.9, 1.7}, {1.7, 1.7}} {
			rsz(ix(cellPt[0]), iy(cellPt[1]), int32(px(cellPt[0]+3.4)-px(cellPt[0])), int32(py(cellPt[1]+3.4)-py(cellPt[1])), int32(1.1*u))
		}

	case "clock": // 表盘 + 指针
		strokePoly(c, circlePts(px(0), py(0), 4.9*u, 14, 0, 360), true, 2, color)
		ln(0, 0, 0, -3.1, 2)
		ln(0, 0, 2.2, 1.3, 2)
		c.Dot(ix(0), iy(0), int32(1.05*u), color)

	case "check": // 对号：实心粗勾
		fillPoly(c, []pt{
			{px(-5.5), py(0.5)}, {px(-4.0), py(-1.3)}, {px(-1.6), py(1.6)},
			{px(4.6), py(-4.9)}, {px(5.7), py(-3.4)}, {px(-1.6), py(4.6)},
		}, color)

	case "close": // 叉号：两条粗斜线
		ln(-3.9, -3.9, 3.9, 3.9, 3)
		ln(-3.9, 3.9, 3.9, -3.9, 3)

	case "star": // 实心五角星（置顶）
		starPath(c, cx, cy, int32(4.9*u), color, true)

	case "star-off": // 空心五角星（未置顶）
		starPath(c, cx, cy, int32(4.9*u), color, false)

	case "up": // 发送：粗箭头
		fillPoly(c, []pt{
			{px(-1.75), py(-0.8)}, {px(-4.3), py(-0.8)}, {px(0), py(-6.2)},
			{px(4.3), py(-0.8)}, {px(1.75), py(-0.8)}, {px(1.75), py(5.6)}, {px(-1.75), py(5.6)},
		}, color)

	case "stop": // 停止：实心圆角方块
		rsz(ix(-3.4), iy(-3.4), int32(px(3.4)-px(-3.4)), int32(py(3.4)-py(-3.4)), int32(1.6*u))

	case "caret-right": // 右三角
		fillPoly(c, []pt{{px(-2.1), py(-3.9)}, {px(3.7), py(0)}, {px(-2.1), py(3.9)}}, color)

	case "caret-down": // 下三角
		fillPoly(c, []pt{{px(-3.9), py(-2.0)}, {px(3.9), py(-2.0)}, {px(0), py(3.6)}}, color)

	case "image": // 图片：圆角相框 + 太阳 + 山峰
		strokePoly(c, roundedRect(px(-5.5), py(-4.4), 11.0*u, 8.8*u, 1.6*u, 3), true, 2, color)
		c.Dot(ix(-2.3), iy(-1.6), int32(1.05*u), color)
		strokePoly(c, []pt{{px(-4.4), py(3.1)}, {px(-1.0), py(-0.5)}, {px(1.3), py(2.6)}, {px(3.1), py(0.7)}, {px(4.6), py(3.1)}}, false, 2, color)

	case "camera": // 截图：机身（上左峰）+ 镜头圆点
		strokePoly(c, []pt{
			{px(-5.3), py(-2.6)}, {px(-2.6), py(-2.6)}, {px(-1.5), py(-4.4)}, {px(1.5), py(-4.4)}, {px(2.6), py(-2.6)}, {px(5.3), py(-2.6)},
			{px(5.3), py(4.0)}, {px(-5.3), py(4.0)},
		}, true, 2, color)
		c.Dot(ix(0), iy(0.7), int32(1.65*u), color)

	case "slash": // 命令：粗斜杠
		ln(2.3, -5.6, -2.3, 5.6, 3)

	case "coin": // 成本：圆环 + ￥ 竖线与双横
		strokePoly(c, circlePts(px(0), py(0), 4.9*u, 14, 0, 360), true, 2, color)
		ln(0, -2.5, 0, 2.5, 2)
		ln(-1.7, -1.3, 1.7, -1.3, 2)
		ln(-1.7, 1.3, 1.7, 1.3, 2)

	case "folder": // 文件夹：实心剪影（带标签）
		fillPoly(c, []pt{
			{px(-6), py(-3.2)}, {px(-2.2), py(-3.2)}, {px(-0.9), py(-5.1)}, {px(6), py(-5.1)},
			{px(6), py(4.6)}, {px(-6), py(4.6)},
		}, color)

	case "model": // 模型：方芯 + 四根引脚
		strokePoly(c, roundedRect(px(-4.3), py(-4.3), 8.6*u, 8.6*u, 1.6*u, 3), true, 2, color)
		c.Dot(ix(0), iy(0), int32(1.35*u), color)
		ln(0, -6.4, 0, -4.6, 2)
		ln(0, 6.4, 0, 4.6, 2)
		ln(-6.4, 0, -4.6, 0, 2)
		ln(6.4, 0, 4.6, 0, 2)

	case "terminal": // 终端：外框 + > 提示符 + 光标线
		strokePoly(c, roundedRect(px(-5.8), py(-4.6), 11.6*u, 9.2*u, 1.7*u, 3), true, 2, color)
		ln(-3.4, -1.9, -1.5, 0, 2)
		ln(-1.5, 0, -3.4, 1.9, 2)
		ln(0.1, 1.9, 3.5, 1.9, 2)

	case "doc": // 文档：折角纸张 + 两条正文线
		strokePoly(c, []pt{
			{px(-4.4), py(-6.0)}, {px(2.1), py(-6.0)}, {px(4.6), py(-3.5)},
			{px(4.6), py(6.0)}, {px(-4.4), py(6.0)},
		}, true, 2, color)
		ln(2.1, -6.0, 2.1, -3.5, 2)
		ln(2.1, -3.5, 4.6, -3.5, 2)
		ln(-2.3, -0.8, 2.7, -0.8, 2)
		ln(-2.3, 1.8, 2.7, 1.8, 2)

	case "edit": // 铅笔：实心剪影
		fillPoly(c, capsulePts(px(-4.5), py(4.5), px(3.1), py(-3.1), 2.6*u), color)
		fillPoly(c, []pt{{px(3.1), py(-3.1)}, {px(4.9), py(-4.9)}, {px(1.1), py(-4.9)}}, color)

	case "chat": // 气泡：实心圆角气泡 + 尾巴
		rsz(ix(-5.4), iy(-4.6), int32(px(5.4)-px(-5.4)), int32(py(3.6)-py(-4.6)), int32(1.7*u))
		fillPoly(c, []pt{{px(-2.4), py(2.4)}, {px(0.3), py(6.6)}, {px(2.8), py(2.4)}}, color)

	case "bolt": // 闪电：实心剪影
		fillPoly(c, []pt{
			{px(0.8), py(-6.4)}, {px(-3.9), py(0.8)}, {px(-0.1), py(0.8)},
			{px(-0.8), py(6.4)}, {px(3.9), py(-0.8)}, {px(0.1), py(-0.8)},
		}, color)

	case "agents": // 派发树：一个头节点 + 两个从节点
		c.Dot(ix(0), iy(-4.7), int32(1.25*u), color)
		c.Dot(ix(-4.1), iy(3.8), int32(1.15*u), color)
		c.Dot(ix(4.1), iy(3.8), int32(1.15*u), color)
		ln(0, -2.6, 0, 0.4, 2)
		ln(0, 0.4, -3.0, 2.9, 2)
		ln(0, 0.4, 3.0, 2.9, 2)

	case "bell": // 铃：实心剪影（顶钮 + 圆顶 + 铃口 + 铃舌）
		c.Dot(ix(0), iy(-5.0), int32(0.95*u), color)
		fillPoly(c, []pt{
			{px(-1.9), py(-3.8)}, {px(-3.5), py(0.1)}, {px(-5.2), py(0.1)}, {px(-5.9), py(2.6)},
			{px(5.9), py(2.6)}, {px(5.2), py(0.1)}, {px(3.5), py(0.1)}, {px(1.9), py(-3.8)},
		}, color)
		c.Dot(ix(0), iy(4.1), int32(1.05*u), color)

	case "layers": // 分组：上下两个尖角 + 中间折叠线（折叠组标识）
		strokePoly(c, []pt{{px(-5.4), py(-3.0)}, {px(0), py(-6.0)}, {px(5.4), py(-3.0)}}, false, 2, color)
		strokePoly(c, []pt{{px(-5.4), py(0.4)}, {px(0), py(-2.6)}, {px(5.4), py(0.4)}}, false, 2, color)
		strokePoly(c, []pt{{px(-5.4), py(3.8)}, {px(0), py(0.8)}, {px(5.4), py(3.8)}}, false, 2, color)
	}
}

// starPath 五角星（实心填满 / 空心描边）。
func starPath(c *ui.Canvas, cx, cy, r int32, color uint32, fill bool) {
	pts := [10][2]float32{}
	for i := 0; i < 10; i++ {
		rad := float32(r)
		if i%2 == 1 {
			rad *= 0.42
		}
		ang := -1.5707963 + float32(i)*0.6283185
		pts[i][0] = float32(cx) + rad*cosf(ang)
		pts[i][1] = float32(cy) + rad*sinf(ang)
	}
	for i := 0; i < 10; i++ {
		a := pts[i]
		b := pts[(i+1)%10]
		c.LineW(int32(a[0]), int32(a[1]), int32(b[0]), int32(b[1]), color, 2)
	}
	if !fill {
		return
	}
	// 实心：按行扫描填内部
	for y := cy - r; y <= cy+r; y++ {
		minx, maxx := int32(1<<30), int32(-1<<30)
		fy := float32(y)
		for i := 0; i < 10; i++ {
			a := pts[i]
			b := pts[(i+1)%10]
			if (a[1] <= fy && b[1] >= fy) || (b[1] <= fy && a[1] >= fy) {
				if a[1] == b[1] {
					continue
				}
				t := (fy - a[1]) / (b[1] - a[1])
				x := int32(a[0] + t*(b[0]-a[0]))
				if x < minx {
					minx = x
				}
				if x > maxx {
					maxx = x
				}
			}
		}
		if maxx > minx {
			c.LineW(minx, y, maxx, y, color, 2)
		}
	}
}

func cosf(x float32) float32 { return float32(math.Cos(float64(x))) }
func sinf(x float32) float32 { return float32(math.Sin(float64(x))) }

func sqrtf(x float32) float32 {
	if x <= 0 {
		return 0
	}
	return float32(math.Sqrt(float64(x)))
}
