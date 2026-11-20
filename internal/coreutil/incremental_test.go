package coreutil

import "testing"

func TestReusePlan(t *testing.T) {
	// 未变化：全部复用
	got := ReusePlan([]uint64{1, 2, 3}, []uint64{1, 2, 3}, false)
	for i, ok := range got {
		if !ok {
			t.Fatalf("第 %d 段应复用", i)
		}
	}

	// 只有尾段变化：前两段复用，尾段重建
	got = ReusePlan([]uint64{1, 2, 3}, []uint64{1, 2, 9}, false)
	if !got[0] || !got[1] || got[2] {
		t.Fatalf("尾段变化时应只重建尾段: %v", got)
	}

	// 消息增加：新段必须重建，老段复用
	got = ReusePlan([]uint64{1, 2}, []uint64{1, 2, 3}, false)
	if !got[0] || !got[1] || got[2] {
		t.Fatalf("新增消息应只重建新段: %v", got)
	}

	// 历史被截断（新建对话/回滚）：长度变短不越界，且对应段复用
	got = ReusePlan([]uint64{1, 2, 3}, []uint64{1, 2}, false)
	if len(got) != 2 || !got[0] || !got[1] {
		t.Fatalf("缩短时应复用前缀: %v", got)
	}

	// 全局条件变化（宽度/字号/主题）：全部重建
	got = ReusePlan([]uint64{1, 2, 3}, []uint64{1, 2, 3}, true)
	for i, ok := range got {
		if ok {
			t.Fatalf("full 模式第 %d 段不应复用", i)
		}
	}

	// 首次排版：prev 为空，全部重建
	got = ReusePlan(nil, []uint64{1}, false)
	if len(got) != 1 || got[0] {
		t.Fatalf("首次排版不应复用: %v", got)
	}
}
