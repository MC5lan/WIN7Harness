//go:build windows

package kit

import "win7harness/internal/ui"

// CanvasAdapter 把 *ui.Canvas 包装成 kit 的 Painter。
type CanvasAdapter struct{ C *ui.Canvas }

func NewCanvasAdapter(c *ui.Canvas) Painter { return CanvasAdapter{C: c} }

func (a CanvasAdapter) FillRect(r ui.Rect, color uint32) { a.C.FillRect(r, color) }
func (a CanvasAdapter) Round(r ui.Rect, radius int32, color uint32) {
	a.C.Round(r, radius, color)
}
func (a CanvasAdapter) RoundStroke(r ui.Rect, radius int32, color uint32) {
	a.C.RoundStroke(r, radius, color)
}
func (a CanvasAdapter) Line(x1, y1, x2, y2 int32, color uint32) {
	a.C.Line(x1, y1, x2, y2, color)
}
func (a CanvasAdapter) LineW(x1, y1, x2, y2 int32, color uint32, width int32) {
	a.C.LineW(x1, y1, x2, y2, color, width)
}
func (a CanvasAdapter) HLine(x, y, w int32, color uint32) { a.C.HLine(x, y, w, color) }
func (a CanvasAdapter) VLine(x, y, h int32, color uint32) { a.C.VLine(x, y, h, color) }
func (a CanvasAdapter) Dot(cx, cy, r int32, color uint32) { a.C.Dot(cx, cy, r, color) }
func (a CanvasAdapter) Ring(cx, cy, radius int32, color uint32) {
	a.C.Ring(cx, cy, radius, color)
}
func (a CanvasAdapter) Text(x, y int32, s string, size, weight int32, color uint32) {
	a.C.Text(x, y, s, size, weight, color)
}
func (a CanvasAdapter) TextFace(x, y int32, s string, size, weight int32, color uint32, face string) {
	a.C.TextFace(x, y, s, size, weight, color, face)
}
func (a CanvasAdapter) TextRect(r ui.Rect, s string, size, weight int32, color uint32, flags uint32) {
	a.C.TextRect(r, s, size, weight, color, flags)
}
func (a CanvasAdapter) TextRectFace(r ui.Rect, s string, size, weight int32, color uint32, flags uint32, face string) {
	a.C.TextRectFace(r, s, size, weight, color, flags, face)
}
func (a CanvasAdapter) MeasureFace(s string, size, weight int32, face string) int32 {
	return a.C.MeasureFace(s, size, weight, face)
}
func (a CanvasAdapter) Clip(r ui.Rect)              { a.C.Clip(r) }
func (a CanvasAdapter) PopClip()                    { a.C.PopClip() }
func (a CanvasAdapter) LineHeight(size int32) int32 { return a.C.LineHeight(size) }
