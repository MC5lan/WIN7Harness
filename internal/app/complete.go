package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"win7harness/internal/codeidx"
	"win7harness/internal/core"
	"win7harness/internal/ui"
)

// @ 文件引用补全：输入 @ 后按前缀列出工作区文件，Tab/Enter 补全路径。
// 纯本地扫描，无三方依赖，适配 Win7 自绘界面。

const (
	compMaxRows = 8
	compMaxFile = 2000
)

type compItem struct {
	Rel  string // 相对工作目录的路径（/ 分隔）；符号项为 "file:line"
	Dir  bool
	Sym  bool   // 来自代码索引的符号候选
	Note string // 符号项补充说明，如 "func parseWork"
}

var compSkipDirs = map[string]bool{
	".git": true, ".svn": true, ".hg": true, ".idea": true, ".vscode": true,
	"node_modules": true, "vendor": true, "__pycache__": true, ".venv": true,
	"dist": true, "build": true, "target": true, ".cache": true,
}

// atToken 返回光标处正在输入的 @ 片段（不含 @），ok 为 false 表示当前没在补全上下文。
func (a *App) atToken() (string, int, bool) {
	if a.inputCur <= 0 || a.inputCur > len(a.input) {
		return "", 0, false
	}
	i := a.inputCur - 1
	for i >= 0 {
		r := a.input[i]
		if r == ' ' || r == '\n' || r == '\t' {
			return "", 0, false
		}
		if r == '@' {
			return string(a.input[i+1 : a.inputCur]), i, true
		}
		i--
	}
	return "", 0, false
}

// refreshComplete 依据当前光标重新计算候选列表。
func (a *App) refreshComplete() {
	frag, at, ok := a.atToken()
	if !ok || (a.eng == nil) {
		a.comp = nil
		a.compIdx = 0
		return
	}
	// 片段含空格或已过长则不再提示
	if len(frag) > 64 {
		a.comp = nil
		return
	}
	root := a.eng.WorkDir
	lp := strings.ToLower(strings.ReplaceAll(frag, "\\", "/"))
	// 已经是 "file:123" 形态时，路径匹配只看冒号前的部分
	filePart := lp
	if i := strings.LastIndex(lp, ":"); i > 0 && isAllDigits(lp[i+1:]) {
		filePart = lp[:i]
	}
	all := a.scanWorkFiles(root)

	// 符号候选：@ 后跟名字片段时，从代码索引里直接给函数/类型/方法，选中即定位到行。
	var symItems []compItem
	if ix := codeidx.Cached(); ix != nil && len(filePart) >= 2 {
		for _, sp := range ix.Search(filePart, 4) {
			symItems = append(symItems, compItem{
				Rel:  fmt.Sprintf("%s:%d", sp.File, sp.Line),
				Sym:  true,
				Note: sp.Kind + " " + sp.Name,
			})
		}
	}

	items := make([]compItem, 0, compMaxRows)
	for _, it := range all {
		low := strings.ToLower(it.Rel)
		if filePart == "" || strings.HasPrefix(low, filePart) || strings.Contains(low, "/"+filePart) || strings.Contains(low, filePart) {
			items = append(items, it)
		}
	}
	// 排序：前缀命中优先，其次层级浅、名字短
	sort.Slice(items, func(i, j int) bool {
		pi := strings.HasPrefix(strings.ToLower(items[i].Rel), filePart)
		pj := strings.HasPrefix(strings.ToLower(items[j].Rel), filePart)
		if pi != pj {
			return pi
		}
		di := strings.Count(items[i].Rel, "/")
		dj := strings.Count(items[j].Rel, "/")
		if di != dj {
			return di < dj
		}
		return len(items[i].Rel) < len(items[j].Rel)
	})
	items = append(symItems, items...)
	if len(items) > compMaxRows {
		items = items[:compMaxRows]
	}
	a.compAt = at
	a.comp = items
	if a.compIdx >= len(a.comp) {
		a.compIdx = 0
	}
}

// acceptComplete 用选中项替换光标处的 @ 片段。
func (a *App) acceptComplete() bool {
	if len(a.comp) == 0 {
		return false
	}
	if a.compIdx < 0 || a.compIdx >= len(a.comp) {
		a.compIdx = 0
	}
	it := a.comp[a.compIdx]
	path := it.Rel
	if it.Dir && !it.Sym {
		path += "/"
	}
	if strings.ContainsAny(path, " ") {
		path = strings.ReplaceAll(path, " ", "\\ ")
	}
	tail := []rune(string(a.input[a.inputCur:]))
	head := append([]rune{}, a.input[:a.compAt+1]...)
	head = append(head, []rune(path)...)
	a.input = append(head, tail...)
	a.inputCur = len(head)
	a.comp = nil
	a.compIdx = 0
	return true
}

// paintComplete 在输入框上方绘制候选浮层。
func (a *App) paintComplete(u *UI, x, y, w int32) {
	if len(a.comp) == 0 {
		return
	}
	c := u.c
	th := a.th
	rowH := int32(22)
	h := rowH*int32(len(a.comp)) + 12
	py := y - h - 6
	if py < 60 {
		py = 60
	}
	r := ui.R(x+20, py, w-40, h)
	c.Round(r, 8, th.InputBg)
	c.RoundStroke(r, 8, th.InputBorder)
	c.TextFace(r.X+10, r.Y+4, "文件引用（上下键选择 · Tab 补全 · Esc 关闭）", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	for i, it := range a.comp {
		ry := r.Y + 18 + int32(i)*rowH
		label := it.Rel
		if it.Dir {
			label += "/"
		}
		col := th.Text
		if i == a.compIdx {
			c.Round(ui.R(r.X+4, ry-1, r.W-8, rowH-2), 6, th.AccentSoft)
			c.RoundStroke(ui.R(r.X+4, ry-1, r.W-8, rowH-2), 6, ui.Blend(th.AccentSoft, th.Accent, 0.5))
		}
		c.TextFace(r.X+10, ry, label, ui.FsXs, 400, col, ui.FaceUI)
		if it.Note != "" {
			nx := r.X + 10 + int32(len([]rune(label)))*7 + 8
			c.TextFace(nx, ry, it.Note, ui.FsXs, 400, th.TextMute, ui.FaceUI)
		}
	}
}

// scanWorkFiles 返回当前缓存的文件清单；缓存过期时在后台重扫，绝不在 UI 线程遍历磁盘。
// @ 补全在大型仓库上第一次按下就会走这里，过去 2000 个文件的 Walk 足以让输入卡住
// 一两秒；现在 UI 只读缓存，扫描完成后再刷新候选。
func (a *App) scanWorkFiles(root string) []compItem {
	a.compCacheMu.Lock()
	var cached []compItem
	if a.compCacheRoot == root {
		cached = a.compCache
	}
	stale := a.compCacheRoot != root || a.compCache == nil || time.Since(a.compCacheAt) >= 60*time.Second
	a.compCacheMu.Unlock()
	if stale {
		a.startWorkFilesScan(root)
	}
	return cached
}

// startWorkFilesScan 启动一次后台文件清单扫描（同一时刻只跑一个）。
func (a *App) startWorkFilesScan(root string) {
	if root == "" {
		return
	}
	a.compCacheMu.Lock()
	if a.compScanBusy && a.compCacheRoot == root {
		a.compCacheMu.Unlock()
		return
	}
	a.compScanBusy = true
	a.compCacheRoot = root
	a.compCacheMu.Unlock()
	core.SafeGo("app/complete.go:scanWorkFiles", func() {
		items := make([]compItem, 0, 256)
		count := 0
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || p == root {
				return nil
			}
			if info.IsDir() && compSkipDirs[info.Name()] {
				return filepath.SkipDir
			}
			if count >= compMaxFile {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return nil
			}
			count++
			items = append(items, compItem{Rel: filepath.ToSlash(rel), Dir: info.IsDir()})
			return nil
		})
		a.compCacheMu.Lock()
		if a.compCacheRoot == root {
			a.compCache = items
			a.compCacheAt = time.Now()
			a.compScanBusy = false
		}
		a.compCacheMu.Unlock()
		select {
		case a.compScanDone <- struct{}{}:
		default:
		}
	})
}

// isAllDigits 判断整串是否都是数字。
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
