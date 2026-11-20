//go:build windows

package app

import (
	"sort"
	"strings"

	"win7harness/internal/core"
)

// 会话检索与管理：搜索、重命名、置顶。
// 会话一多，左栏就变成一列同名标题，"翻历史"实际不可用，所以这里补三件事：
// 按标题与正文过滤、就地改名、把常用会话钉在顶部。

// sortConvs 按"置顶优先 + 最近活动倒序"排列。
func sortConvs(list []*Conv) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Pinned != list[j].Pinned {
			return list[i].Pinned
		}
		return list[i].At.After(list[j].At)
	})
}

// convMatch 描述一次会话搜索命中。
type convMatch struct {
	idx int    // a.convs 下标
	sip string // 命中的正文片段（标题命中时为空）
}

// searchConvs 按关键字过滤会话，空关键字返回全部。
// 匹配范围：标题 + 全部消息正文（用户与助手都算），不区分大小写。
func (a *App) searchConvs(q string) []convMatch {
	q = strings.TrimSpace(q)
	out := make([]convMatch, 0, len(a.convs))
	if q == "" {
		for i := range a.convs {
			out = append(out, convMatch{idx: i})
		}
		return out
	}
	needle := strings.ToLower(q)
	for i, cv := range a.convs {
		if strings.Contains(strings.ToLower(cv.Title), needle) {
			out = append(out, convMatch{idx: i})
			continue
		}
		if !cv.Loaded {
			// 启动只读了索引：先用标题/预览匹配，点开后再走全文。
			if p := strings.Index(strings.ToLower(cv.Preview), needle); p >= 0 {
				out = append(out, convMatch{idx: i, sip: clipSnippet(cv.Preview, p, len(needle))})
			}
			continue
		}
		if s := convSnippet(cv, needle); s != "" {
			out = append(out, convMatch{idx: i, sip: s})
		}
	}
	return out
}

// convSnippet 在会话正文里找关键字，返回前后各留一点上下文的片段。
func convSnippet(cv *Conv, needle string) string {
	for _, m := range cv.Msgs {
		low := strings.ToLower(m.Text)
		p := strings.Index(low, needle)
		if p < 0 {
			// 工具调用的参数里也可能有关键字，一并覆盖
			for _, t := range m.Tools {
				if strings.Contains(strings.ToLower(t.Args), needle) {
					return clipSnippet(t.Args, strings.Index(strings.ToLower(t.Args), needle), len(needle))
				}
			}
			continue
		}
		return clipSnippet(m.Text, p, len(needle))
	}
	return ""
}

// clipSnippet 以命中位置为中心截取一段可读文本（按字节定位，再回退到字符边界）。
func clipSnippet(s string, at, n int) string {
	if at < 0 {
		at = 0
	}
	start := at - 12
	if start < 0 {
		start = 0
	}
	// 回退到 UTF-8 字符起始字节，避免截出半个汉字
	for start > 0 && start < len(s) && (s[start]&0xC0) == 0x80 {
		start--
	}
	end := at + n + 20
	if end > len(s) {
		end = len(s)
	}
	for end < len(s) && (s[end]&0xC0) == 0x80 {
		end++
	}
	seg := s[start:end]
	seg = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(seg)
	seg = strings.TrimSpace(seg)
	if start > 0 {
		seg = "…" + seg
	}
	if end < len(s) {
		seg += "…"
	}
	return seg
}

// togglePin 切换某个会话的置顶状态，并立即落盘。
// 置顶变化会改变列表顺序，所以同步修正 active 下标，避免"钉完之后跳到别的会话"。
func (a *App) togglePin(i int) {
	a.sessDirty = true
	if i < 0 || i >= len(a.convs) {
		return
	}
	if !a.convs[i].Loaded {
		id := a.convs[i].ID
		a.loadConvAsync(id, func(*Conv) {
			if j := a.convIndexByID(id); j >= 0 {
				a.togglePin(j)
			}
		})
		return
	}
	a.saveConv() // 先把手头会话存了，再动顺序
	cur := a.currentConv()
	cv := a.convs[i]
	cv.Pinned = !cv.Pinned
	sortConvs(a.convs)
	if cur != nil {
		for k, c := range a.convs {
			if c == cur {
				a.active = k
				break
			}
		}
	}
	a.saveConv()
	// 置顶的会话不一定是当前会话：saveConv 只写当前会话，必须单独把这个
	// 会话的 Pinned 标记落盘，否则重启后置顶会消失。
	a.saveConvFile(cv)
	core.Logf("会话 %s 置顶=%v", cv.ID, cv.Pinned)
	if cv.Pinned {
		a.toast("已置顶「"+cv.Title+"」", false)
	} else {
		a.toast("已取消置顶「"+cv.Title+"」", false)
	}
}

// renameConv 修改会话标题并落盘。空标题视为取消。
func (a *App) renameConv(i int, title string) {
	if i < 0 || i >= len(a.convs) {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	if !a.convs[i].Loaded {
		id := a.convs[i].ID
		a.loadConvAsync(id, func(*Conv) {
			if j := a.convIndexByID(id); j >= 0 {
				a.renameConv(j, title)
			}
		})
		return
	}
	r := []rune(title)
	if len(r) > 60 {
		title = string(r[:60])
	}
	a.convs[i].Title = title
	if a.currentConv() == a.convs[i] {
		a.saveConv()
	} else {
		a.saveConvFile(a.convs[i])
	}
	core.Logf("会话 %s 重命名为 %q", a.convs[i].ID, title)
}
