package core

import (
	"sort"
	"strings"
	"time"
)

// 会话全文检索。
//
// 左栏那个搜索框是"过滤会话列表"，一屏只能看到每个会话的第一处命中。
// 真正要回答"我上次是在哪句话里提过那个报错"时，需要的是：命中在哪条消息、
// 周围长什么样、按相关度排好。这里只做纯计算，界面层负责取数与跳转。

// FindMsg 参与检索的一条消息。
type FindMsg struct {
	Role string // user / assistant
	Text string
	// Extra 是附加文本（工具参数、工具输出），单独放是为了让片段能标注来源。
	Extra string
	At    time.Time
}

// FindDoc 一篇可检索的文档，对应一个会话。
type FindDoc struct {
	ID    string
	Title string
	At    time.Time
	Msgs  []FindMsg
}

// FindHit 一处命中。
type FindHit struct {
	DocID   string
	Title   string
	MsgIdx  int
	Role    string
	At      time.Time
	Snippet string
	// InTool 表示命中来自工具参数/输出而非对话正文。
	InTool bool
	// Score 是该会话的总命中次数，用于排序。
	Score int
	// MsgAt 是该条消息的时间，用于同时命中时比新旧。
	MsgAt time.Time
}

// splitQuery 把查询按空白切成多个关键词，去重并转小写。
// 多个词之间是"与"关系：都得出现才算命中，避免长查询被一个虚词带偏。
func splitQuery(q string) []string {
	fields := strings.Fields(strings.ToLower(q))
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// Find 在全部文档里检索，返回按相关度排序的命中。
//
// 多个关键词是"与"：同一条消息里全部出现才算命中——查 "connection reset" 时，
// 只提到 connection 的消息会淹掉真正要找的那条报错。
// limit<=0 表示不限制。同一个关键词在同一条消息里只算一处命中。
func Find(docs []FindDoc, query string, limit int) []FindHit {
	words := splitQuery(query)
	if len(words) == 0 {
		return nil
	}

	var hits []FindHit
	for _, doc := range docs {
		// 标题命中给加权：标题是人工摘要，命中标题通常就是用户要找的那个会话
		titleLow := strings.ToLower(doc.Title)
		titleHits := 0
		for _, w := range words {
			if strings.Contains(titleLow, w) {
				titleHits++
			}
		}
		titleScore := 0
		if titleHits == len(words) {
			titleScore = 3 * len(words)
		}

		var docHits []FindHit
		for mi, m := range doc.Msgs {
			textLow := strings.ToLower(m.Text)
			extraLow := strings.ToLower(m.Extra)
			firstAt, firstLen := -1, 0
			var firstSrc string
			firstInExtra := false
			all := true
			for _, w := range words {
				pText := strings.Index(textLow, w)
				pExtra := strings.Index(extraLow, w)
				if pText < 0 && pExtra < 0 {
					all = false
					break
				}
				if firstAt < 0 {
					if pText >= 0 {
						firstAt, firstLen, firstSrc = pText, len(w), m.Text
					} else {
						firstAt, firstLen, firstSrc, firstInExtra = pExtra, len(w), m.Extra, true
					}
				}
			}
			if !all {
				continue
			}
			at := m.At
			if at.IsZero() {
				at = doc.At
			}
			docHits = append(docHits, FindHit{
				DocID: doc.ID, Title: doc.Title, MsgIdx: mi, Role: m.Role, At: at,
				MsgAt: at, InTool: firstInExtra,
				Snippet: clipSnippet(firstSrc, firstAt, firstLen),
			})
		}

		// 只有标题命中、正文一条都没命中的会话也要露出来，否则用户搜自己起的标题会搜不到
		if len(docHits) == 0 && titleScore > 0 {
			docHits = append(docHits, FindHit{
				DocID: doc.ID, Title: doc.Title, MsgIdx: -1, Role: "title",
				At: doc.At, MsgAt: doc.At, Snippet: doc.Title,
			})
		}
		score := titleScore + len(docHits)
		for i := range docHits {
			docHits[i].Score = score
		}
		hits = append(hits, docHits...)
	}

	// 相关度高的会话排前面，同分时新消息优先——找"最近那次"比找半年前那次更常见
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if !hits[i].MsgAt.Equal(hits[j].MsgAt) {
			return hits[i].MsgAt.After(hits[j].MsgAt)
		}
		if hits[i].DocID != hits[j].DocID {
			return hits[i].DocID > hits[j].DocID
		}
		return hits[i].MsgIdx < hits[j].MsgIdx
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// clipSnippet 以命中位置为中心截一段可读文本。
// 命中位置是按字节算的，截断时要回退/前进到 UTF-8 字符边界，否则会切出半个汉字。
func clipSnippet(s string, at, n int) string {
	if at < 0 {
		at = 0
	}
	if at > len(s) {
		at = len(s)
	}
	start := at - 16
	if start < 0 {
		start = 0
	}
	for start > 0 && start < len(s) && (s[start]&0xC0) == 0x80 {
		start--
	}
	end := at + n + 40
	if end > len(s) {
		end = len(s)
	}
	for end < len(s) && (s[end]&0xC0) == 0x80 {
		end++
	}
	seg := strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(s[start:end])
	seg = strings.TrimSpace(seg)
	if start > 0 {
		seg = "…" + seg
	}
	if end < len(s) {
		seg += "…"
	}
	return seg
}
