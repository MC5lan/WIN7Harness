//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/ui"
	"win7harness/internal/win32"
)

// codeBlockEntry 保存本帧某个代码块的可复制原文，key 为 "<消息下标>:<块下标>"。
type codeBlockEntry struct {
	Key  string
	Lang string
	Text string
}

// drawCodeActions 在代码块悬停时浮出「复制 / 保存」两个小按钮。
// 调用点排在 drawMsgActions / drawMsgLinks 之后，保证按钮命中区优先级最高。
func (a *App) drawCodeActions(u *UI, it visItem, dx, dy int32, clip ui.Rect) {
	if len(it.codeBlocks) == 0 {
		return
	}
	for _, cb := range it.codeBlocks {
		if cb.First < 0 || cb.Last >= len(it.lines) {
			continue
		}
		first, last := it.lines[cb.First], it.lines[cb.Last]
		y := dy + first.Y
		h := last.Y + last.H - first.Y
		if y+h < clip.Y || y > clip.Bottom() || h <= 0 {
			continue
		}
		// 命中区必须裁到可视区：代码块滚出视口后 y 会是负数，
		// 不裁的话它的命中区会一路顶进顶栏，把右上角按钮的点击吞掉。
		zoneY, zoneH := y, h
		if zoneY < clip.Y {
			zoneH -= clip.Y - zoneY
			zoneY = clip.Y
		}
		if bottom := zoneY + zoneH; bottom > clip.Bottom() {
			zoneH = clip.Bottom() - zoneY
		}
		if zoneH <= 0 {
			continue
		}
		zone := ui.R(clip.X+24, zoneY, clip.W, zoneH)
		if !u.Hit(fmt.Sprintf("codezone:%d:%d", it.msgIdx, cb.Block), zone) {
			continue
		}
		// 按钮放在首行偏右，但给最右侧的语言标签留出约 62px，避免相互遮挡。
		bx := clip.Right() - 170
		by := y + 2
		if by < clip.Y+2 {
			by = clip.Y + 2
		}
		key := fmt.Sprintf("%d:%d", it.msgIdx, cb.Block)
		a.codeBlocks = append(a.codeBlocks, codeBlockEntry{Key: key, Lang: cb.Lang, Text: cb.Text})
		if u.Button(fmt.Sprintf("codeact.copy:%d:%d", it.msgIdx, cb.Block), "复制", ui.R(bx, by, 50, 20), BtnSubtle) {
		}
		if u.Button(fmt.Sprintf("codeact.save:%d:%d", it.msgIdx, cb.Block), "保存", ui.R(bx+56, by, 50, 20), BtnSubtle) {
		}
	}
}

func (a *App) codeBlockByKey(key string) (codeBlockEntry, bool) {
	for _, e := range a.codeBlocks {
		if e.Key == key {
			return e, true
		}
	}
	return codeBlockEntry{}, false
}

// copyCodeBlock 复制代码块原始文本（非高亮后的 Runs）。
func (a *App) copyCodeBlock(mi, bi int) {
	e, ok := a.codeBlockByKey(fmt.Sprintf("%d:%d", mi, bi))
	if !ok || e.Text == "" {
		return
	}
	if win32.SetClipboardText(e.Text) {
		a.toast("代码已复制", false)
	} else {
		a.toast("复制失败：剪贴板被占用", true)
	}
}

// saveCodeBlock 把代码块保存到工作区，保存后在资源管理器里定位。
func (a *App) saveCodeBlock(mi, bi int) {
	e, ok := a.codeBlockByKey(fmt.Sprintf("%d:%d", mi, bi))
	if !ok || e.Text == "" {
		return
	}
	wd := a.sessionWorkDir()
	if wd == "" {
		a.toast("尚未设置工作目录", true)
		return
	}
	ext := map[string]string{
		"go": ".go", "py": ".py", "js": ".js", "jsx": ".jsx", "ts": ".ts", "tsx": ".tsx",
		"rs": ".rs", "c": ".c", "h": ".h", "cpp": ".cpp", "java": ".java", "rb": ".rb",
		"php": ".php", "cs": ".cs", "swift": ".swift", "kt": ".kt", "md": ".md",
		"json": ".json", "yaml": ".yaml", "yml": ".yml", "xml": ".xml", "html": ".html",
		"css": ".css", "sh": ".sh", "bat": ".bat", "ps1": ".ps1", "sql": ".sql",
	}
	suffix := ext[strings.ToLower(e.Lang)]
	if suffix == "" {
		suffix = ".txt"
	}
	p := core.ResolvePath(wd, "snippet"+suffix)
	if core.IsForbiddenPath(p) || core.EscapesWorkDir(wd, p) {
		a.toast("保存路径不在工作区内", true)
		return
	}
	// 同名文件已存在时加序号，避免覆盖用户文件。
	for i := 2; ; i++ {
		if _, err := os.Stat(p); err != nil {
			break
		}
		p = core.ResolvePath(wd, fmt.Sprintf("snippet-%d%s", i, suffix))
	}
	if err := core.WriteFileAtomic(p, []byte(e.Text), 0644); err != nil {
		a.toast("保存失败："+err.Error(), true)
		return
	}
	revealPath(p, true)
	a.toast("已保存到 "+filepath.Base(p), false)
}
