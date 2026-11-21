//go:build windows

package app

import (
	"strings"
	"testing"
	"time"
)

func TestTimelineEvents(t *testing.T) {
	now := time.Now()
	a := &App{msgs: []Message{
		{Role: "user", Text: "修复构建", At: now.Add(-3 * time.Minute)},
		{Role: "assistant", Kind: "notice", Text: "上下文已自动压缩", At: now.Add(-2 * time.Minute)},
		{Role: "assistant", Kind: "error", Err: "编译失败", At: now.Add(-time.Minute)},
		{Role: "assistant", At: now, Tools: []*ToolCard{
			{Name: "edit_file", Args: `{"path":"a.go"}`, Done: true},
			{Name: "run_tests", Args: `{}`, Done: true, Err: true},
		}},
	}}
	got := a.timelineDebug()
	for _, want := range []string{"任务", "上下文已自动压缩", "编译失败", "修改文件", "运行测试失败"} {
		if !strings.Contains(got, want) {
			t.Fatalf("时间线缺少 %q：\n%s", want, got)
		}
	}
	if strings.Index(got, "任务") > strings.Index(got, "运行测试失败") {
		t.Fatalf("时间线应从旧到新：\n%s", got)
	}
}
