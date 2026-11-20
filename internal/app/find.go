//go:build windows

package app

import (
	"fmt"
	"strings"
	"time"

	"win7harness/internal/core"
)

// 会话全文检索的界面侧适配：把内存里的会话喂给 core.Find，
// 再把结果映射回 a.convs 下标，供 /open 跳转。

// findHit 一条可在界面里跳转的命中。
type findHit struct {
	ConvIdx int
	MsgIdx  int // -1 表示只命中标题
	Title   string
	Role    string
	Snippet string
	At      time.Time
	Score   int
}

// buildFindDocs 把当前会话列表转成检索文档。
// 工具卡片也参与检索：报错原文与命令参数往往只出现在工具输出里，
// 漏了它们就会出现"我明明看到过这句报错"却搜不到的情况。
func (a *App) buildFindDocs() []core.FindDoc {
	docs := make([]core.FindDoc, 0, len(a.convs))
	for _, cv := range a.convs {
		if cv == nil {
			continue
		}
		doc := core.FindDoc{ID: cv.ID, Title: cv.Title, At: cv.At, Msgs: make([]core.FindMsg, 0, len(cv.Msgs))}
		for _, m := range cv.Msgs {
			var extra strings.Builder
			for _, t := range m.Tools {
				extra.WriteString(t.Name)
				extra.WriteString(" ")
				extra.WriteString(t.Args)
				extra.WriteString("\n")
				extra.WriteString(t.Output)
				extra.WriteString("\n")
			}
			doc.Msgs = append(doc.Msgs, core.FindMsg{
				Role: m.Role, Text: m.Text, Extra: extra.String(), At: m.At,
			})
		}
		docs = append(docs, doc)
	}
	return docs
}

// FindAll 跨全部会话检索，返回可直接展示的命中列表。
func (a *App) FindAll(q string, limit int) []findHit {
	// 启动后只载入了最近会话；第一次检索先把剩余正文丢后台补全，
	// 并明确告诉用户本次不是全量，避免"我明明记得有这句话却搜不到"。
	if ids := a.unloadedConvIDs(); len(ids) > 0 {
		a.sysNote(fmt.Sprintf("还有 %d 个历史会话正文未载入，正在后台补全；本次 /find 只覆盖已载入部分，稍后重试即可搜索全部。", len(ids)))
		if time.Since(a.lastHydrateAll) > 30*time.Second {
			a.lastHydrateAll = time.Now()
			core.SafeGo("app/find.go:hydrate", func() { a.hydrateConvs(ids) })
		}
	}
	raw := core.Find(a.buildFindDocs(), q, limit)
	if len(raw) == 0 {
		return nil
	}
	idxByID := make(map[string]int, len(a.convs))
	for i, cv := range a.convs {
		if cv != nil {
			idxByID[cv.ID] = i
		}
	}
	out := make([]findHit, 0, len(raw))
	for _, h := range raw {
		ci, ok := idxByID[h.DocID]
		if !ok {
			continue
		}
		out = append(out, findHit{ConvIdx: ci, MsgIdx: h.MsgIdx, Title: h.Title,
			Role: h.Role, Snippet: h.Snippet, At: h.At, Score: h.Score})
	}
	return out
}

// OpenFindHit 跳到第 n 条命中（从 1 开始）：切换会话并把视图滚到那条消息。
func (a *App) OpenFindHit(n int) {
	if n < 1 || n > len(a.findHits) {
		a.sysNote(fmt.Sprintf("没有第 %d 条结果。先 /find 关键词，再 /open 序号。", n))
		return
	}
	a.openFindHit(a.findHits[n-1])
}

// openHitView 供检索结果卡片直接调用：卡片本身带着目标会话与消息下标，
// 不需要用户先记住序号。
func (a *App) openHitView(h HitView) {
	a.openFindHit(findHit{ConvIdx: h.ConvIdx, MsgIdx: h.MsgIdx, Title: h.Title, Snippet: h.Snippet})
}

// openFindHit 按一条命中跳转，并点亮目标消息。
func (a *App) openFindHit(h findHit) {
	if h.ConvIdx < 0 || h.ConvIdx >= len(a.convs) {
		a.sysNote("那条结果对应的会话已经不在了。")
		return
	}
	if h.ConvIdx != a.active {
		a.active = h.ConvIdx
		a.loadConv()
		a.ver++
	}
	if h.MsgIdx < 0 {
		// 只命中标题：没有可定位的消息，回到会话顶部即可
		a.scroll = 0
	} else {
		a.jumpMsg = h.MsgIdx
		a.hlMsg = h.MsgIdx
		a.hlAt = time.Now()
	}
	a.autoScroll = false
	a.ver++
	a.layout()
	a.toast("已定位到命中位置", false)
	core.Logf("检索跳转：会话 %s 消息 %d", h.Title, h.MsgIdx)
}

// unloadedConvIDs 返回尚未读入正文的会话 ID。
func (a *App) unloadedConvIDs() []string {
	var ids []string
	for _, cv := range a.convs {
		if cv != nil && !cv.Loaded {
			ids = append(ids, cv.ID)
		}
	}
	return ids
}

// msgTopOffset 返回某条消息在会话坐标系里的起始 y。
// 依赖上次排版结果（laySegs），所以调用点必须排在 layoutSession 之后。
func (a *App) msgTopOffset(idx int) int32 {
	if idx < 0 || idx >= len(a.laySegs) {
		return -1
	}
	return a.laySegs[idx].y
}
