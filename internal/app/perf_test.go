package app

import (
	"testing"
	"time"

	"win7harness/internal/ui/kit"
)

func TestWheelNotchesAccumulatesHighResolution(t *testing.T) {
	a := &App{perf: newPerfState()}
	if got := a.wheelNotches(40); got != 0 {
		t.Fatalf("40 增量应先积累，实际 %d", got)
	}
	if got := a.wheelNotches(40); got != 0 {
		t.Fatalf("第二个 40 仍应积累，实际 %d", got)
	}
	if got := a.wheelNotches(40); got != 1 {
		t.Fatalf("第三个 40 应凑成一格，实际 %d", got)
	}
	if got := a.wheelNotches(-120); got != -1 {
		t.Fatalf("标准反向一格应为 -1，实际 %d", got)
	}
	if got := a.wheelNotches(240); got != 2 {
		t.Fatalf("240 应为两格，实际 %d", got)
	}
}

func TestSnapshotConvIsDeepCopy(t *testing.T) {
	now := time.Now()
	orig := &Conv{
		ID:    "c1",
		Title: "标题",
		At:    now,
		Msgs: []Message{{
			Role:   "assistant",
			Text:   "第一段",
			Images: []string{"a.png"},
			Hits:   []HitView{{Title: "命中", MsgIdx: 1}},
			Tools:  []*ToolCard{{ID: "t1", Name: "read_file", Output: "old"}},
		}},
	}
	snap := snapshotConv(orig)
	orig.Msgs[0].Text = "被 UI 继续追加"
	orig.Msgs[0].Images[0] = "b.png"
	orig.Msgs[0].Hits[0].Title = "改掉"
	orig.Msgs[0].Tools[0].Output = "new"
	if snap.Msgs[0].Text != "第一段" {
		t.Fatalf("正文被共享：%q", snap.Msgs[0].Text)
	}
	if snap.Msgs[0].Images[0] != "a.png" {
		t.Fatalf("图片切片被共享：%v", snap.Msgs[0].Images)
	}
	if snap.Msgs[0].Hits[0].Title != "命中" {
		t.Fatalf("命中切片被共享：%v", snap.Msgs[0].Hits)
	}
	if snap.Msgs[0].Tools[0].Output != "old" {
		t.Fatalf("工具卡指针被共享：%v", snap.Msgs[0].Tools[0])
	}
}

func TestMsgTopOffsetUsesSegmentY(t *testing.T) {
	a := &App{laySegs: []laySeg{
		{y: 8, h: 100},
		{y: 128, h: 50},
		{y: 198, h: 20},
	}}
	if got := a.msgTopOffset(0); got != 8 {
		t.Fatalf("第 0 段偏移 = %d", got)
	}
	if got := a.msgTopOffset(2); got != 198 {
		t.Fatalf("第 2 段偏移 = %d", got)
	}
	if got := a.msgTopOffset(3); got != -1 {
		t.Fatalf("越界应返回 -1，实际 %d", got)
	}
}

func TestKitScrollClampAndPage(t *testing.T) {
	c := kit.New(nil)
	st := c.Store("k")
	st.ScrollMax = 500
	st.ViewH = 200
	if !kit.ScrollBy(c, "k", 120) || st.Scroll != 120 {
		t.Fatalf("ScrollBy 失败：%d", st.Scroll)
	}
	if !kit.ScrollBy(c, "k", 9999) || st.Scroll != 500 {
		t.Fatalf("上界未夹紧：%d", st.Scroll)
	}
	if !kit.ScrollPage(c, "k", -1) || st.Scroll != 340 {
		t.Fatalf("PageUp 步长不对：%d", st.Scroll)
	}
	if !kit.ScrollHome(c, "k") || st.Scroll != 0 {
		t.Fatalf("Home 未归零：%d", st.Scroll)
	}
	if !kit.ScrollEnd(c, "k") || st.Scroll != 500 {
		t.Fatalf("End 未到底：%d", st.Scroll)
	}
}
