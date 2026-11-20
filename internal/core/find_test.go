package core

import (
	"testing"
	"time"
)

func mkDoc(id, title string, at time.Time, msgs ...FindMsg) FindDoc {
	return FindDoc{ID: id, Title: title, At: at, Msgs: msgs}
}

func TestFindRanksAndLimits(t *testing.T) {
	base := time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local)
	docs := []FindDoc{
		mkDoc("c1", "旧会话", base.Add(-48*time.Hour),
			FindMsg{Role: "user", Text: "编译报错 undefined symbol", At: base.Add(-48 * time.Hour)},
			FindMsg{Role: "assistant", Text: "这个 undefined 是因为少链接了库", At: base.Add(-47 * time.Hour)},
		),
		mkDoc("c2", "新会话", base,
			FindMsg{Role: "user", Text: "另一个话题，顺便提到 undefined", At: base},
		),
		mkDoc("c3", "无关", base, FindMsg{Role: "user", Text: "今天天气不错", At: base}),
	}

	hits := Find(docs, "undefined", 0)
	if len(hits) != 3 {
		t.Fatalf("应有 3 处命中，实际 %d", len(hits))
	}
	// c1 命中两次，相关度最高，应排第一
	if hits[0].DocID != "c1" || hits[0].Score != 2 {
		t.Fatalf("排序或计分不对: %+v", hits[0])
	}
	if hits[len(hits)-1].DocID != "c2" {
		t.Fatalf("低分应排最后: %+v", hits[len(hits)-1])
	}
	if hits[0].Snippet == "" {
		t.Fatalf("片段缺失: %+v", hits[0])
	}
	// 同分会话内，较新的消息排更前
	if hits[0].MsgIdx != 1 {
		t.Fatalf("同分应新消息优先: %+v", hits[0])
	}
	if hits[1].Role != "user" {
		t.Fatalf("两条都应返回: %+v", hits)
	}

	if got := Find(docs, "undefined", 2); len(got) != 2 {
		t.Fatalf("limit 未生效: %d", len(got))
	}
}

func TestFindMultiWordAndCase(t *testing.T) {
	now := time.Now()
	docs := []FindDoc{
		mkDoc("a", "t", now,
			FindMsg{Role: "user", Text: "ERR_CONNECTION_RESET 出现在登录时", At: now},
			FindMsg{Role: "assistant", Text: "err_connection_reset 是代理问题", At: now},
		),
		mkDoc("b", "t2", now, FindMsg{Role: "user", Text: "只有 connection 没有别的词", At: now}),
	}
	hits := Find(docs, "connection reset", 0)
	if len(hits) != 2 {
		t.Fatalf("两个词都命中的消息才该出现，实际 %d: %+v", len(hits), hits)
	}
	for _, h := range hits {
		if h.DocID != "a" {
			t.Fatalf("b 只命中一个词，应被排除: %+v", h)
		}
	}
	// 大小写不敏感
	if len(Find(docs, "ERR_CONNECTION", 0)) != 2 {
		t.Fatal("大小写敏感了")
	}
	// 空查询返回空
	if len(Find(docs, "   ", 0)) != 0 {
		t.Fatal("空查询应无结果")
	}
}

func TestFindToolExtraAndSnippetBoundary(t *testing.T) {
	now := time.Now()
	long := "前面的内容很长很长很长很长很长很长很长很长很长很长很长很长"
	docs := []FindDoc{mkDoc("c", "标题", now,
		FindMsg{Role: "assistant", Text: "", Extra: `{"path":"D:\\报表\\明细.xlsx","err":"权限不足"}`, At: now},
		FindMsg{Role: "user", Text: long + "权限不足" + long, At: now},
	)}
	hits := Find(docs, "权限不足", 0)
	if len(hits) != 2 {
		t.Fatalf("正文与工具输出都该命中，实际 %d", len(hits))
	}
	var inTool bool
	for _, h := range hits {
		if h.InTool {
			inTool = true
		}
		if h.Snippet == "" {
			t.Fatal("片段不能为空")
		}
		// 不能切出非法 UTF-8
		for _, r := range h.Snippet {
			if r == '\uFFFD' {
				t.Fatalf("片段切坏了: %q", h.Snippet)
			}
		}
	}
	if !inTool {
		t.Fatal("应有一处命中标记为来自工具输出")
	}
}

func TestFindTitleWeight(t *testing.T) {
	now := time.Now()
	docs := []FindDoc{
		mkDoc("titled", "日报模板整理", now, FindMsg{Role: "user", Text: "随便说说", At: now}),
		mkDoc("body", "闲聊", now, FindMsg{Role: "user", Text: "日报模板", At: now}),
	}
	hits := Find(docs, "日报模板", 0)
	if len(hits) != 2 {
		t.Fatalf("标题与该正文各应有命中，实际 %d: %+v", len(hits), hits)
	}
	if hits[0].DocID != "titled" {
		t.Fatalf("标题命中应加权排前: %+v", hits)
	}
	if hits[0].MsgIdx != -1 {
		t.Fatalf("纯标题命中应标记 MsgIdx=-1: %+v", hits[0])
	}
	// 标题 3 分 + 该会话内的这处命中 1 分
	if hits[0].Score != 4 {
		t.Fatalf("标题命中应计 3 分再叠加命中数，实际 %d", hits[0].Score)
	}
}
