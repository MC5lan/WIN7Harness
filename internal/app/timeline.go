//go:build windows

package app

import (
	"fmt"
	"strings"
	"time"

	"win7harness/internal/ui"
)

// 会话时间线：从最近消息里抽出“压缩、审批、技能、验证、错误”等关键节点，
// 给右侧检查器一个比纯工具列表更好读的进度视图。只读现有消息，不额外落盘。

type timelineItem struct {
	at   time.Time
	kind string
	text string
}

var timelineTools = map[string]string{
	"edit_file":   "修改文件",
	"write_file":  "写入文件",
	"apply_patch": "应用补丁",
	"run_tests":   "运行测试",
	"run_command": "执行命令",
	"task":        "子任务",
	"skill_use":   "加载技能",
	"todo_write":  "更新计划",
}

func (a *App) timelineEvents(limit int) []timelineItem {
	if limit <= 0 || len(a.msgs) == 0 {
		return nil
	}
	var out []timelineItem
	add := func(at time.Time, kind, text string) {
		text = strings.TrimSpace(oneLine(text))
		if text == "" || len(out) >= limit {
			return
		}
		out = append(out, timelineItem{at: at, kind: kind, text: text})
	}
	for i := len(a.msgs) - 1; i >= 0 && len(out) < limit; i-- {
		m := &a.msgs[i]
		at := m.At
		if at.IsZero() {
			at = time.Now()
		}
		if m.Kind == "budget" || m.Kind == "error" || m.Err != "" {
			msg := strings.TrimSpace(m.Err)
			if msg == "" {
				msg = m.Text
			}
			add(at, "error", "错误 · "+msg)
			continue
		}
		if m.Kind == "notice" {
			add(at, "notice", m.Text)
			continue
		}
		if m.Role == "user" && m.Kind == "" {
			add(at, "user", "任务 · "+m.Text)
			continue
		}
		// 工具只挑会产生进度语义的几类；读文件、搜索不刷屏。
		for j := len(m.Tools) - 1; j >= 0 && len(out) < limit; j-- {
			t := m.Tools[j]
			label, ok := timelineTools[t.Name]
			if !ok {
				continue
			}
			s := label
			if t.Err {
				s += "失败"
			} else if !t.Done {
				s += "进行中"
			}
			if arg := ArgSummary(t.Name, t.Args); arg != "" {
				s += " · " + arg
			}
			add(at, "tool", s)
		}
	}
	// 从旧到新排列，最后一条在最下面。
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (a *App) paintTimeline(u *UI, x, w int32, H, yy int32) int32 {
	items := a.timelineEvents(8)
	if len(items) == 0 {
		return yy
	}
	c := u.c
	th := a.th
	c.TextFace(x+16, yy, "最近事件", ui.FsSm, 700, th.TextDim, ui.FaceUI)
	yy += 20
	for _, it := range items {
		if yy > H-32 {
			break
		}
		col := th.TextMute
		mark := "·"
		switch it.kind {
		case "error":
			col, mark = th.Danger, "!"
		case "notice":
			col, mark = th.Accent, "◆"
		case "user":
			col, mark = ui.CurrentTokens().TextSecondary, "›"
		case "tool":
			col, mark = th.Success, "•"
		}
		c.TextFace(x+16, yy, mark, ui.FsXs, 700, col, ui.FaceUI)
		text := it.text
		if !it.at.IsZero() {
			text = it.at.Format("15:04") + "  " + text
		}
		c.TextFace(x+30, yy, ellipsis(c, text, ui.FsXs, 400, w-46), ui.FsXs, 400, th.TextDim, ui.FaceUI)
		yy += 18
	}
	yy += 4
	c.HLine(x+16, yy-6, w-32, th.BorderSoft)
	return yy + 4
}

// 供调试/测试：把时间线渲染成一行行文本。
func (a *App) timelineDebug() string {
	var b strings.Builder
	for _, it := range a.timelineEvents(20) {
		fmt.Fprintf(&b, "%s %s %s\n", it.at.Format("15:04"), it.kind, it.text)
	}
	return strings.TrimSpace(b.String())
}
