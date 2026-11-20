//go:build windows

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/ui"
	"win7harness/internal/win32"
)

// pathTokenRe 匹配消息正文里出现的路径 token。
// 保守匹配：必须有目录分隔符或点号扩展名，且可带 :行 / :起-止 后缀。
// 文本片段由 markdown Span 切分后落在 Run.Text 里，反引号已被 ParseInline 去掉。
var pathTokenRe = regexp.MustCompile(`(?:(?:[A-Za-z]:[\\/])?[\w.@\-]+(?:[\\/][\w.@\-]+)+|[\w.@\-]+\.[A-Za-z0-9]{1,8})(?::\d+(?:-\d+)?)?`)

// msgLink 是一条消息里检测到的可点击路径。
type msgLink struct {
	Raw  string // 原始 token（可能带 :行）
	Abs  string // 解析后的绝对路径
	From int    // 起始行，0 表示未指定
	To   int
	Rect ui.Rect // 本帧屏幕坐标下的命中区
}

// sessionWorkDir 返回当前会话工作目录，引擎未就绪时退回配置里的默认目录。
func (a *App) sessionWorkDir() string {
	if a.eng != nil && a.eng.WorkDir != "" {
		return a.eng.WorkDir
	}
	if a.cfg != nil {
		return a.cfg.WorkDir
	}
	return ""
}

// isURLTail 判断匹配起点前面是否紧邻 URL 协议前缀。
// 正则只会匹配到 example.com/a.go:12 这一段，需要靠前缀识别它是 URL 的一部分而非本地文件。
func isURLTail(text string, start int) bool {
	from := start - 16
	if from < 0 {
		from = 0
	}
	return strings.Contains(text[from:start], "://")
}

// looksLikePathToken 过滤明显的非路径（URL、纯标点等）。
func looksLikePathToken(tok string) bool {
	if tok == "" || len(tok) > 260 {
		return false
	}
	low := strings.ToLower(tok)
	if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") ||
		strings.HasPrefix(low, "www.") || strings.Contains(tok, "://") {
		return false
	}
	return strings.ContainsAny(tok, `/\`) || strings.Contains(tok, ".")
}

type pathCacheEntry struct {
	exists bool
	isDir  bool
	at     time.Time
}

const pathCacheTTL = 3 * time.Second

// pathStateCached 返回路径是否存在/是否为目录。UI 线程专用。
func (a *App) pathStateCached(p string) (bool, bool) {
	if a.pathCache == nil {
		a.pathCache = map[string]pathCacheEntry{}
	}
	if e, ok := a.pathCache[p]; ok && time.Since(e.at) < pathCacheTTL {
		return e.exists, e.isDir
	}
	st, err := os.Stat(p)
	e := pathCacheEntry{exists: err == nil, isDir: err == nil && st.IsDir(), at: time.Now()}
	if len(a.pathCache) > 512 {
		a.pathCache = map[string]pathCacheEntry{}
	}
	a.pathCache[p] = e
	return e.exists, e.isDir
}

// drawMsgLinks 扫描 assistant 消息的排版结果，给真实存在的文件路径注册点击命中区并画悬停下划线。
// 必须在 drawMsgActions 之后调用：后者注册的 msgzone 覆盖整条消息，而 HitAt 后注册者优先。
func (a *App) drawMsgLinks(u *UI, it visItem, dx, dy int32, clip ui.Rect) {
	if it.msg == nil || len(it.lines) == 0 {
		return
	}
	wd := a.sessionWorkDir()
	if wd == "" {
		return
	}
	c := u.c
	th := a.th

	for _, ln := range it.lines {
		yy := dy + ln.Y
		if yy > clip.Bottom()+40 || yy+ln.H < clip.Y-40 {
			continue
		}
		pad := int32(0)
		if ln.IsCode() {
			pad = 12
		}
		for _, run := range ln.Runs {
			matches := pathTokenRe.FindAllStringIndex(run.Text, -1)
			if len(matches) == 0 {
				continue
			}
			face := ui.FaceUI
			if run.Mono {
				face = ui.FaceMono
			}
			weight := int32(400)
			if run.Bold {
				weight = 700
			}
			ty := yy + (ln.H-run.Size)/2 - 2
			for _, m := range matches {
				tok := run.Text[m[0]:m[1]]
				if isURLTail(run.Text, m[0]) || !looksLikePathToken(tok) {
					continue
				}
				rel, from, to := core.ParsePathLineRef(tok)
				abs := core.ResolvePath(wd, filepath.FromSlash(rel))
				if core.IsForbiddenPath(abs) || core.EscapesWorkDir(wd, abs) {
					continue
				}
				exists, isDir := a.pathStateCached(abs)
				if !exists || isDir {
					continue
				}
				prefixW := c.MeasureFace(run.Text[:m[0]], run.Size, weight, face)
				tw := c.MeasureFace(tok, run.Size, weight, face)
				if tw <= 0 {
					continue
				}
				x := dx + run.X + pad + prefixW
				r := ui.R(x-2, ty-2, tw+4, run.Size+6)
				if r.H < 16 {
					r.H = 16
				}
				// 只给完全落在可视区里的 token 注册，避免滚动区外的命中区抢点击。
				if r.X < clip.X || r.Right() > clip.Right() || r.Y < clip.Y || r.Bottom() > clip.Bottom() {
					continue
				}
				id := fmt.Sprintf("path:%d", len(a.msgLinks))
				hover := u.Hit(id, r)
				a.msgLinks = append(a.msgLinks, msgLink{Raw: tok, Abs: abs, From: from, To: to, Rect: r})
				if hover {
					c.HLine(x, ty+run.Size+1, tw, ui.Alpha(th.Bg, th.Accent, 0.9))
				}
			}
		}
	}
}

// openPathAtLine 用外部程序打开路径；有行号时优先 VS Code 的 -g 跳行。
func (a *App) openPathAtLine(abs string, from int) {
	if abs == "" {
		return
	}
	wd := a.sessionWorkDir()
	if core.IsForbiddenPath(abs) || core.EscapesWorkDir(wd, abs) {
		a.toast("路径不在工作区内", true)
		return
	}
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() {
		a.toast("文件不存在："+abs, true)
		return
	}
	for _, name := range []string{"code", "code.cmd"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		var args []string
		if from > 0 {
			args = []string{"-g", fmt.Sprintf("%s:%d", abs, from)}
		} else {
			args = []string{abs}
		}
		if err := exec.Command(p, args...).Start(); err == nil {
			if from > 0 {
				a.toast(fmt.Sprintf("已在编辑器打开（第 %d 行）", from), false)
			} else {
				a.toast("已在编辑器打开", false)
			}
			return
		}
	}
	if win32.ShellOpen(abs) {
		if from > 0 {
			a.toast("已用默认程序打开；跳行需安装 VS Code", false)
		}
	}
}
