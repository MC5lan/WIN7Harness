package kit

import (
	"strings"
	"testing"
)

// mockPainter 记录绘制调用，用于无头测试。
type mockPainter struct {
	fills  [][2]interface{}
	texts  []string
	rounds [][4]int32
	lines  [][4]int32
}

func (m *mockPainter) FillRect(r Rect, color uint32) {
	m.fills = append(m.fills, [2]interface{}{r, color})
}
func (m *mockPainter) Round(r Rect, radius int32, color uint32) {
	m.rounds = append(m.rounds, [4]int32{r.X, r.Y, r.W, r.H})
}
func (m *mockPainter) RoundStroke(r Rect, radius int32, color uint32) {}
func (m *mockPainter) Line(x1, y1, x2, y2 int32, color uint32) {
	m.lines = append(m.lines, [4]int32{x1, y1, x2, y2})
}
func (m *mockPainter) LineW(x1, y1, x2, y2 int32, color uint32, width int32) {
	m.lines = append(m.lines, [4]int32{x1, y1, x2, y2})
}
func (m *mockPainter) HLine(x, y, w int32, color uint32) {
	m.lines = append(m.lines, [4]int32{x, y, x + w, y})
}
func (m *mockPainter) VLine(x, y, h int32, color uint32) {
	m.lines = append(m.lines, [4]int32{x, y, x, y + h})
}
func (m *mockPainter) Dot(cx, cy, r int32, color uint32)       {}
func (m *mockPainter) Ring(cx, cy, radius int32, color uint32) {}
func (m *mockPainter) Text(x, y int32, s string, size, weight int32, color uint32) {
	m.texts = append(m.texts, s)
}
func (m *mockPainter) TextFace(x, y int32, s string, size, weight int32, color uint32, face string) {
	m.texts = append(m.texts, s)
}
func (m *mockPainter) TextRect(r Rect, s string, size, weight int32, color uint32, flags uint32) {
	m.texts = append(m.texts, s)
}
func (m *mockPainter) TextRectFace(r Rect, s string, size, weight int32, color uint32, flags uint32, face string) {
	m.texts = append(m.texts, s)
}
func (m *mockPainter) MeasureFace(s string, size, weight int32, face string) int32 {
	// 近似度量：拉丁字符 0.62 字宽，CJK 1.0 字宽
	w := 0.0
	for _, r := range s {
		if r > 0x2E7F {
			w += float64(size)
		} else {
			w += float64(size) * 0.62
		}
	}
	return int32(w)
}
func (m *mockPainter) Clip(r Rect)                 {}
func (m *mockPainter) PopClip()                    {}
func (m *mockPainter) LineHeight(size int32) int32 { return size + size/2 }

func TestColumnLayout(t *testing.T) {
	m := &mockPainter{}
	c := New(m)
	root := Column(8,
		Label("标题", 16, 700, 0xFFFFFF),
		Label("正文正文正文", 13, 400, 0xCCCCCC),
	)
	size := root.Measure(c, Constraint{MaxW: 800, MaxH: 800})
	wantH := int32(24) + 8 + int32(19) // LineHeight(16)=24, gap 8, LineHeight(13)=19
	if size.H != wantH {
		t.Fatalf("列高度 = %d，期望 %d", size.H, wantH)
	}
	root.Arrange(c, R(0, 0, 800, size.H))
	root.Paint(c, m)

	// 断言两个 label 都被画了（texts 按顺序出现）
	found := 0
	for _, s := range m.texts {
		if s == "标题" || s == "正文正文正文" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("应绘制两个标签，实际 %v", m.texts)
	}
}

func TestRowExpandPushesToEnd(t *testing.T) {
	m := &mockPainter{}
	c := New(m)
	root := Row(8,
		Label("左", 13, 400, 0xFFFFFF),
		Spacer(),
		Label("右", 13, 400, 0xFFFFFF),
	)
	row, ok := root.(*row)
	if !ok {
		t.Fatal("根节点应为 *row")
	}
	sizes, total := row.layout(c, 400)
	// 左(26px) + gap + spacer(remainder) + gap + 右(26px) → 铺满 400
	if total.W != 400 {
		t.Fatalf("行总宽 = %d，期望 400", total.W)
	}
	spacerW := sizes[1].W
	labelR := sizes[2].W
	if spacerW != 400-13-8-8-13 {
		t.Fatalf("spacer 宽度 = %d，期望 %d", spacerW, 400-13-8-8-13)
	}
	_ = labelR
}

func TestMouseUpPressMiss(t *testing.T) {
	m := &mockPainter{}
	c := New(m)
	clkA, clkB := false, false
	root := Row(8,
		Button("a", "A", ButtonPrimary, func() { clkA = true }),
		Button("b", "B", ButtonPrimary, func() { clkB = true }),
	)
	size := root.Measure(c, Constraint{MaxW: 400, MaxH: 100})
	root.Arrange(c, R(0, 0, 400, size.H))
	root.Paint(c, m)

	// 按下 A（Paint 注册的命中区生效）
	if !c.MouseDown(8, 10, 0) {
		t.Fatal("按下未命中任何控件")
	}
	// 抬起位置在 B 上：不应触发
	if c.MouseUp(60, 10, 0) {
		t.Fatal("跨控件抬起不应派发点击")
	}
	if clkA || clkB {
		t.Fatalf("跨控件抬起触发了点击 A=%v B=%v", clkA, clkB)
	}

	// 按下 A、抬起 A：触发 A
	if !c.MouseDown(8, 10, 0) {
		t.Fatal("再次按下未命中")
	}
	if !c.MouseUp(8, 10, 0) {
		t.Fatal("同控件抬起未派发点击")
	}
	if !clkA || clkB {
		t.Fatalf("期望 A 被点击，实际 A=%v B=%v", clkA, clkB)
	}
}

func TestMouseUpRightClickNoDispatch(t *testing.T) {
	m := &mockPainter{}
	c := New(m)
	clk := false
	root := Row(8, Button("a", "A", ButtonPrimary, func() { clk = true }))
	size := root.Measure(c, Constraint{MaxW: 400, MaxH: 100})
	root.Arrange(c, R(0, 0, 400, size.H))
	root.Paint(c, m)
	c.MouseDown(8, 10, 1) // 右键
	c.MouseUp(8, 10, 1)
	if clk {
		t.Fatal("右键不应派发点击")
	}
}

func TestFocusOrder(t *testing.T) {
	m := &mockPainter{}
	c := New(m)
	root := Column(4,
		Input("in1", "placeholder", 64, nil),
		Button("b1", "确定", ButtonPrimary, nil),
		ButtonDisabled("b2", "禁用", ButtonSecondary),
		Button("b3", "取消", ButtonGhost, nil),
	)
	size := root.Measure(c, Constraint{MaxW: 400, MaxH: 400})
	root.Arrange(c, R(0, 0, 400, size.H))
	root.Paint(c, m)

	order := []string{"in1", "b1", "b3"}
	for i, want := range order {
		got := c.Focus()
		if i == 0 {
			// 初始焦点应为空
			if got != "" {
				t.Fatalf("初始焦点 = %q，期望空", got)
			}
		}
		c.MoveFocus(false)
		if c.Focus() != want {
			t.Fatalf("第 %d 次 Tab 焦点 = %q，期望 %q", i+1, c.Focus(), want)
		}
		_ = got
	}
	// 禁用控件不在 Tab 序列里
	for _, id := range c.focusOrder {
		if id == "b2" {
			t.Fatal("禁用控件出现在 Tab 序列中")
		}
	}
	// Shift+Tab 回退
	c.MoveFocus(true)
	if c.Focus() != "b1" {
		t.Fatalf("Shift+Tab 后焦点 = %q，期望 b1", c.Focus())
	}
}

func TestStorePersists(t *testing.T) {
	m := &mockPainter{}
	c := New(m)
	st := c.Store("k")
	st.Text = "hello"
	st2 := c.Store("k")
	if st2.Text != "hello" {
		t.Fatalf("Store 未保持状态：%q", st2.Text)
	}
}

func TestScrollClamp(t *testing.T) {
	m := &mockPainter{}
	c := New(m)
	child := SizedBox(300, 500, Label("内容", 13, 400, 0xFFFFFF))
	scroll := Scroll("list", 200, child)
	// 第一次布局：childH=500 > 视口 200 → maxScroll=300
	measureAndArrange := func() {
		size := scroll.Measure(c, Constraint{MinW: 0, MaxW: 400, MinH: 0, MaxH: 400})
		scroll.Arrange(c, R(0, 0, 400, size.H))
	}
	measureAndArrange()
	st := c.Store("list")
	// ScrollBy 正方向
	ScrollBy(c, "list", 350)
	measureAndArrange()
	if st.Scroll != 300 {
		t.Fatalf("滚动 = %d，期望夹紧到 300", st.Scroll)
	}
	ScrollBy(c, "list", -999)
	measureAndArrange()
	if st.Scroll != 0 {
		t.Fatalf("滚动 = %d，期望夹回 0", st.Scroll)
	}
}

func TestToggleClick(t *testing.T) {
	m := &mockPainter{}
	c := New(m)
	fired := false
	onChange := func(on bool) { fired = on }
	tog := Toggle("t1", onChange)
	size := tog.Measure(c, Constraint{MaxW: 100, MaxH: 100})
	tog.Arrange(c, R(0, 0, size.W, size.H))
	tog.Paint(c, m)

	if ToggleValue(c, "t1") {
		t.Fatal("初始值应为关")
	}
	// 点击中心
	c.MouseDown(20, 11, 0)
	c.MouseUp(20, 11, 0)
	if !ToggleValue(c, "t1") {
		t.Fatal("点击后应为开启")
	}
	if !fired {
		t.Fatal("onChange 未触发")
	}
}

func TestTextTrunc(t *testing.T) {
	m := &mockPainter{}
	c := New(m)
	long := strings.Repeat("汉字", 40)
	got := TextTrunc(c, long, 13, 400, 100, "Microsoft YaHei")
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("应带省略号，实际 %q", got)
	}
	if c.Painter.MeasureFace(got, 13, 400, "Microsoft YaHei") > 100 {
		t.Fatalf("截断后超宽：%d", c.Painter.MeasureFace(got, 13, 400, "Microsoft YaHei"))
	}
	short := "短文本"
	if got := TextTrunc(c, short, 13, 400, 100, "Microsoft YaHei"); got != short {
		t.Fatalf("短文本不应截断：%q", got)
	}
}
