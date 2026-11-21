//go:build windows

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"win7harness/internal/agent"
	"win7harness/internal/core"
)

// 本文件由 commands.go 的 handleSlash 拆分而来（T08），仅移动 case body，未改行为。
// 文件与会话：/clear /compact /undo /checkpoints /rewind /diff /export /rename /pin /find /open。

// slashClear 处理 /clear 及别名。
func (a *App) slashClear(rest string) bool {
	a.newConv()
	a.toast("已新建对话", false)
	return true
}

// slashCompact 处理 /compact 及别名。
func (a *App) slashCompact(rest string) bool {
	if a.eng == nil || a.streaming {
		a.toast("当前正在生成，稍后再试", true)
		return true
	}
	a.statusMsg = "正在压缩上下文…"
	core.SafeGo("app/commands.go:106", func() {
		msg, err := a.eng.Compact(context.Background())
		if err != nil {
			a.evCh <- agent.Event{Kind: "notice", Text: "压缩失败：" + err.Error()}
			return
		}
		a.evCh <- agent.Event{Kind: "notice", Text: msg}
	})
	return true
}

// slashUndo 处理 /undo 及别名。
func (a *App) slashUndo(rest string) bool {
	res, err := core.UndoLast()
	if err != nil {
		a.toast(err.Error(), true)
		return true
	}
	if res.Existed {
		a.sysNote("已回滚到上一次改动之前：" + res.Path)
	} else {
		a.sysNote("已撤销本次新建的文件：" + res.Path)
	}
	return true
}

// slashCheckpoints 处理 /checkpoints 及别名。
func (a *App) slashCheckpoints(rest string) bool {
	a.sysNote(core.FormatCheckpoints(core.ListCheckpointsConv(20, currentConvID(a))))
	return true
}

// slashRewind 处理 /rewind 及别名。
func (a *App) slashRewind(rest string) bool {
	fields := strings.Fields(rest)
	n := 1
	if len(fields) > 0 {
		v, err := strconv.Atoi(fields[0])
		if err != nil || v <= 0 {
			a.toast("用法：/rewind [步数]，例如 /rewind 3", true)
			return true
		}
		n = v
	}
	// 优先走 git 回滚：能一次退回多个提交，且未提交改动会先 stash 保住
	if a.eng != nil && core.IsGitRepo(a.eng.WorkDir) {
		if msg, err := core.GitRewind(a.eng.WorkDir, n); err == nil {
			a.sysNote(msg)
			return true
		} else if core.GitAvailable() {
			a.sysNote("git 回滚未完成，改用文件快照回退：" + err.Error())
		}
	}
	done, err := core.RewindSteps(n)
	if err != nil {
		a.toast(err.Error(), true)
		if len(done) > 0 {
			a.sysNote(core.FormatCheckpoints(core.ListCheckpointsConv(10, currentConvID(a))))
		}
		return true
	}
	var lines []string
	for _, d := range done {
		if d.Existed {
			lines = append(lines, "已恢复："+d.Path)
		} else {
			lines = append(lines, "已删除（本次新建）："+d.Path)
		}
	}
	a.sysNote(fmt.Sprintf("已回退 %d 步：\n%s", len(done), strings.Join(lines, "\n")))
	return true
}

// slashDiff 处理 /diff 及别名。
func (a *App) slashDiff(rest string) bool {
	if a.eng == nil {
		return true
	}
	wd := a.eng.WorkDir
	if _, err := os.Stat(filepath.Join(wd, ".git")); err != nil {
		a.sysNote("当前工作目录不是 git 仓库：\n" + wd + "\n如需版本管理可在该目录执行 git init。")
		return true
	}
	short, err := core.GitRun(wd, "status", "--short")
	if err != nil {
		a.sysNote("git status 失败：" + err.Error())
		return true
	}
	stat, err := core.GitRun(wd, "diff", "--stat")
	if err != nil {
		a.sysNote("git diff 失败：" + err.Error())
		return true
	}
	if strings.TrimSpace(short) == "" {
		a.sysNote("工作区干净，没有未提交的改动。")
		return true
	}
	a.sysNote("未提交改动：\n" + strings.TrimRight(short, "\n") + "\n\n改动统计：\n" + strings.TrimRight(stat, "\n") + "\n\n提示：/commit 提交，或在对话里让我 review。")
	return true
}

// slashRename 处理 /rename 及别名。
func (a *App) slashRename(rest string) bool {
	if rest == "" {
		// 不带标题就进入就地改名，光标直接落在标题上
		a.startRename(a.active)
		return true
	}
	a.renameConv(a.active, rest)
	a.toast("已重命名", false)
	return true
}

// slashPin 处理 /pin 及别名。
func (a *App) slashPin(rest string) bool {
	a.togglePin(a.active)
	return true
}

// slashExport 处理 /export 及别名。
func (a *App) slashExport(rest string) bool {
	conv := a.currentConv()
	if conv == nil || len(conv.Msgs) == 0 {
		a.sysNote("当前会话还没有内容，没什么可导出的。")
		return true
	}
	target := strings.TrimSpace(rest)
	p, err := a.ExportConv(conv, target)
	if err != nil {
		a.sysNote("导出失败：" + err.Error())
		return true
	}
	a.sysNote("会话已导出为 Markdown：\n" + p + "\n可以直接发给同事，或用编辑器改。" +
		"\n想指定目录：/export D:\\笔记")
	revealPath(p, true)
	return true
}

// slashFind 处理 /find 及别名。
func (a *App) slashFind(rest string) bool {
	q := strings.TrimSpace(rest)
	if q == "" {
		a.sysNote("用法：`/find 关键词 [关键词2]`\n\n在全部会话里检索正文与工具输出，多个词用空格分隔（需同时出现）。")
		return true
	}
	hits := a.FindAll(q, 30)
	a.findHits = hits
	if len(hits) == 0 {
		a.sysNote("没有找到包含「" + q + "」的内容（共检索 " + fmt.Sprint(len(a.convs)) + " 个会话）。")
		return true
	}
	views := make([]HitView, 0, len(hits))
	for _, h := range hits {
		title := h.Title
		if title == "" {
			title = "新对话"
		}
		src := "助手答"
		switch h.Role {
		case "title":
			src = "标题"
		case "user":
			src = "你问"
		}
		when := ""
		if !h.At.IsZero() {
			when = h.At.Format("01-02 15:04")
		}
		snippet := strings.Join(strings.Fields(h.Snippet), " ")
		views = append(views, HitView{
			Title: title, Src: src, When: when, Snippet: snippet,
			ConvIdx: h.ConvIdx, MsgIdx: h.MsgIdx,
		})
	}
	a.sysNoteHits(fmt.Sprintf("**「%s」命中 %d 处** · 检索了 %d 个会话", q, len(hits), len(a.convs)), views)
	return true
}

// slashOpen 处理 /open 及别名。
func (a *App) slashOpen(rest string) bool {
	n := 0
	for _, r := range strings.TrimSpace(rest) {
		if r < '0' || r > '9' {
			n = 0
			break
		}
		n = n*10 + int(r-'0')
	}
	if n == 0 {
		a.sysNote("用法：/open 序号（序号来自 /find 的结果列表）")
		return true
	}
	a.OpenFindHit(n)
	return true
}
