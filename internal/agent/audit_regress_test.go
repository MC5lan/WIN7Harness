package agent

import (
	"strings"
	"testing"

	"win7harness/internal/llm"
)

// TestResetClearsLedgers 断言 Reset 清空用量账本、写标志、消息。
func TestResetClearsLedgers(t *testing.T) {
	e := &Engine{Model: "m", WorkDir: t.TempDir()}
	e.Messages = []llm.Message{{Role: "user", Content: "hi"}}
	e.addUsageFor("m", llm.Usage{PromptTokens: 100, CompletionTokens: 20})
	e.compactCount = 2
	e.wroteThisTurn = true
	e.Reset()

	if in, out, turns := e.Usage(); in != 0 || out != 0 || turns != 0 {
		t.Fatalf("Reset 后 Usage 应归零，实际 %d/%d/%d", in, out, turns)
	}
	if got := e.CompactCount(); got != 0 {
		t.Fatalf("Reset 后压缩计数应为 0，实际 %d", got)
	}
	if e.wroteThisTurnNow() {
		t.Fatal("Reset 后写标志应为 false")
	}
	if len(e.Messages) != 0 {
		t.Fatal("Reset 后 Messages 应为空")
	}
	if in, out, turns := e.SubUsage(); in != 0 || out != 0 || turns != 0 {
		t.Fatalf("Reset 后子代理账本应归零，实际 %d/%d/%d", in, out, turns)
	}
}

// TestBuildContextBoundaryNoOrphanTool 断言上下文裁剪后不会以孤儿 tool 消息开头
// （否则 OpenAI 兼容接口一律 400，会话确定性变砖）。
func TestBuildContextBoundaryNoOrphanTool(t *testing.T) {
	e := &Engine{Model: "m", WorkDir: t.TempDir(), ContextWindow: 1} // budget 触发下限 4 万字符
	big := strings.Repeat("填", 45000)
	e.Messages = []llm.Message{
		{Role: "user", Content: big},
		{Role: "assistant", Content: "a1", ToolCalls: []llm.ToolCall{
			{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "read_file", Arguments: `{"path":"a"}`}},
		}},
		{Role: "tool", ToolCallID: "c1", Name: "read_file", Content: "body1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2", ToolCalls: []llm.ToolCall{
			{ID: "c2", Type: "function", Function: llm.FunctionCall{Name: "write_file", Arguments: `{"path":"b","content":"x"}`}},
		}},
		{Role: "tool", ToolCallID: "c2", Name: "write_file", Content: "ok"},
	}
	msgs := e.buildContext()
	// 第一条非 system 消息不能是 tool
	for _, m := range msgs[1:] {
		if m.Role == "tool" {
			t.Fatalf("上下文以孤儿 tool 消息开头（裁剪破坏了协议不变量）")
		}
		break
	}
	// 任一 tool 消息必须有对应的 assistant(tool_calls) 前驱
	seenAssistant := false
	for _, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			seenAssistant = true
		}
		if m.Role == "tool" && !seenAssistant {
			t.Fatalf("tool 消息 %q 之前没有 assistant(tool_calls)（裁剪成孤儿）", m.ToolCallID)
		}
	}
}
