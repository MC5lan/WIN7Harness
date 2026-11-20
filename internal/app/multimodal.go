package app

// 多模态输入：图片附件（粘贴 / 拖入 / 选图）。
// 图片统一落地到附件目录，消息中只存路径；发送时再转换成 data URL。

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/ui"
	"win7harness/internal/win32"
)

// ImageRowHeight 消息气泡内图片行占用的高度。
const ImageRowHeight = 96

// ThumbH 缩略图高度。
const ThumbH = 80

// appImgs 是全局图片缓存：同一张图在输入区与消息区复用同一份位图。
var appImgs = ui.NewImageCache()

// imgDib 加载（并缓存）图片位图。
func imgDib(path string) *ui.DibImage {
	return appImgs.Get(path, func() (image.Image, error) {
		return core.LoadImageFile(path)
	})
}

// thumbRect 计算缩略图在给定起点上的矩形（高度固定，宽度按比例）。
func thumbRect(x, y int32, d *ui.DibImage) ui.Rect {
	w := int32(ThumbH)
	if d != nil && d.H() > 0 {
		w = ThumbH * d.W() / d.H()
	}
	if w < 48 {
		w = 48
	}
	if w > 160 {
		w = 160
	}
	return ui.R(x, y, w, ThumbH)
}

// drawImageRow 在一行内绘制若干缩略图，返回实际画出的张数。
func drawImageRow(c *ui.Canvas, paths []string, x, y, maxW int32, th *ui.Theme) int {
	cx := x
	n := 0
	for _, p := range paths {
		d := imgDib(p)
		r := thumbRect(cx, y, d)
		if n > 0 && cx+r.W > x+maxW {
			break
		}
		if d != nil {
			c.Round(ui.R(r.X-1, r.Y-1, r.W+2, r.H+2), 6, th.Border)
			c.DrawImg(d, r)
			c.RoundStroke(r, 6, th.Border)
		} else {
			c.Round(r, 6, th.InputBg)
			c.RoundStroke(r, 6, th.Border)
			c.TextFace(r.X+8, r.Y+r.H/2-7, "!", ui.FsSm, 700, th.Danger, ui.FaceUI)
		}
		cx += r.W + 8
		n++
	}
	return n
}

// currentConvID 当前会话 ID。
func (a *App) currentConvID() string {
	if cv := a.currentConv(); cv != nil {
		return cv.ID
	}
	return ""
}

// addImageFiles 把一批本地图片文件加入待发附件。
// 非图片文件忽略；超过上限的部分截断并提示。
func (a *App) addImageFiles(paths []string) {
	if len(paths) == 0 {
		return
	}
	convID := a.currentConvID()
	added, skipped := 0, 0
	for _, p := range paths {
		if !core.IsImagePath(p) {
			skipped++
			continue
		}
		if len(a.pendingImgs) >= core.MaxImagesPerMessage {
			skipped++
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			skipped++
			continue
		}
		saved, err := core.SaveAttachment(convID, raw)
		if err != nil {
			skipped++
			continue
		}
		if a.hasPendingImage(saved) {
			continue
		}
		a.pendingImgs = append(a.pendingImgs, saved)
		added++
	}
	a.ver++
	if skipped > 0 {
		a.toast(fmt.Sprintf("已附加 %d 张图片，忽略 %d 项（非图片或超出 %d 张上限）", added, skipped, core.MaxImagesPerMessage), false)
	} else if added > 0 {
		a.toast(fmt.Sprintf("已附加 %d 张图片", added), false)
	}
}

// hasPendingImage 判断附件是否已存在。
func (a *App) hasPendingImage(p string) bool {
	for _, q := range a.pendingImgs {
		if q == p {
			return true
		}
	}
	return false
}

// pasteClipboardImage 尝试从剪贴板粘贴图片，成功返回 true。
func (a *App) pasteClipboardImage() bool {
	dib, ok := win32.GetClipboardDIB()
	if !ok {
		return false
	}
	if len(a.pendingImgs) >= core.MaxImagesPerMessage {
		a.toast(fmt.Sprintf("最多附带 %d 张图片", core.MaxImagesPerMessage), true)
		return true
	}
	saved, err := core.SaveAttachment(a.currentConvID(), dib)
	if err != nil {
		a.toast("粘贴图片失败："+err.Error(), true)
		return true
	}
	if !a.hasPendingImage(saved) {
		a.pendingImgs = append(a.pendingImgs, saved)
	}
	a.ver++
	return true
}

// startSnip 进入框选截图；截完直接进附件区，省掉"截图-存盘-再选图"三步。
func (a *App) startSnip() {
	if a.w == nil {
		return
	}
	dib, w, h, ok := ui.SnipInteractive(a.w.Hwnd())
	if !ok {
		return
	}
	a.attachDIB(dib, fmt.Sprintf("截图 %dx%d", w, h))
}

// onSnipCapture 是 PrintScreen 热键的回调，与工具栏按钮同一条落地路径。
func (a *App) onSnipCapture(dib []byte, w, h int) {
	a.attachDIB(dib, fmt.Sprintf("截图 %dx%d", w, h))
}

// attachDIB 把内存里的 DIB 存成附件并挂到当前会话。
func (a *App) attachDIB(dib []byte, note string) {
	if len(dib) == 0 {
		return
	}
	if len(a.pendingImgs) >= core.MaxImagesPerMessage {
		a.toast(fmt.Sprintf("最多附带 %d 张图片", core.MaxImagesPerMessage), true)
		return
	}
	saved, err := core.SaveAttachment(a.currentConvID(), dib)
	if err != nil {
		a.toast("截图保存失败："+err.Error(), true)
		return
	}
	if !a.hasPendingImage(saved) {
		a.pendingImgs = append(a.pendingImgs, saved)
	}
	a.toast(note+" 已加入附件（也已复制到剪贴板）", false)
	a.ver++
}

// pickImages 弹出系统选图对话框。排队到 Timer，避免在 Paint 里阻塞 UI。
func (a *App) pickImages() {
	a.queueUI(a.pickImagesNow)
}

func (a *App) pickImagesNow() {
	var hwnd win32.HWND
	if a.w != nil {
		hwnd = a.w.Hwnd()
	}
	a.beginNativeDialog()
	files := win32.OpenImageDialog(hwnd, "选择图片")
	a.endNativeDialog()
	if len(files) == 0 {
		return
	}
	a.addImageFiles(files)
}

// DropFiles 实现 ui.DropFilesApp：资源管理器拖入文件时回调。
func (a *App) DropFiles(paths []string) {
	var imgs []string
	for _, p := range paths {
		if core.IsImagePath(p) {
			imgs = append(imgs, p)
		}
	}
	if len(imgs) == 0 {
		// 拖入非图片文件：把路径直接写进输入框，省得手动复制
		if len(paths) > 0 {
			a.insertInput(strings.Join(paths, " "))
			a.ver++
		}
		return
	}
	a.addImageFiles(imgs)
}

// removePendingImage 删除第 i 个待发图片。
func (a *App) removePendingImage(i int) {
	if i < 0 || i >= len(a.pendingImgs) {
		return
	}
	a.pendingImgs = append(a.pendingImgs[:i], a.pendingImgs[i+1:]...)
	a.ver++
}

// clearPendingImages 清空待发图片（切换会话时）。
func (a *App) clearPendingImages() {
	if len(a.pendingImgs) > 0 {
		a.pendingImgs = nil
		a.ver++
	}
}

// pendingImageRects 返回输入区附件 chip 的点击区域（供命中测试与绘制复用）。
func (a *App) pendingImageRects(x, y int32) []ui.Rect {
	out := make([]ui.Rect, 0, len(a.pendingImgs))
	cx := x
	for _, p := range a.pendingImgs {
		d := imgDib(p)
		r := thumbRect(cx, y, d)
		out = append(out, r)
		cx += r.W + 8
	}
	return out
}

// saveConvImages 把消息里的图片路径转成相对附件目录的引用，避免换机器后失效。
// 当前实现保留绝对路径：Win7 上同一用户目录稳定，且便于直接打开。
func imageDisplayName(p string) string {
	return filepath.Base(p)
}
