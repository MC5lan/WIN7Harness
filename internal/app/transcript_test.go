package app

import (
	"strings"
	"testing"

	"win7harness/internal/llm"
)

func TestPruneEngineMsgsKeepsToolPairsAndStripsImages(t *testing.T) {
	msgs := make([]llm.Message, 0, 40)
	for i := 0; i < 20; i++ {
		msgs = append(msgs,
			llm.Message{Role: "user", Content: "任务"},
			llm.Message{Role: "assistant", Content: "调用", ToolCalls: []llm.ToolCall{{
				ID: "t", Type: "function", Function: llm.FunctionCall{Name: "read_file", Arguments: `{"path":"a.go"}`},
			}}},
			llm.Message{Role: "tool", ToolCallID: "t", Name: "read_file", Content: strings.Repeat("结果", 200)},
		)
	}
	msgs = append(msgs, llm.Message{Role: "user", Content: "看图", Parts: []llm.Part{
		{Type: "text", Text: "图"},
		{Type: "image_url", ImageURL: &llm.ImageURL{URL: "data:image/png;base64,AAAA"}},
	}})
	got := pruneEngineMsgs(msgs, 8, 1<<20, 0)
	if len(got) == 0 || len(got) > 10 {
		t.Fatalf("裁剪后条数异常：%d", len(got))
	}
	if got[0].Role == "tool" {
		t.Fatal("裁剪起点不能是孤立的 tool 消息")
	}
	last := got[len(got)-1]
	for _, p := range last.Parts {
		if p.Type == "image_url" {
			t.Fatal("图片 data URL 不应写入会话轨迹")
		}
	}
	if !strings.Contains(last.Content, "历史图片已省略") {
		t.Fatal("去掉图片后应留下说明")
	}
}

func TestPruneEngineMsgsCapsTokens(t *testing.T) {
	msgs := make([]llm.Message, 0, 10)
	for i := 0; i < 10; i++ {
		msgs = append(msgs, llm.Message{Role: "user", Content: strings.Repeat("长上下文", 200)})
	}
	got := pruneEngineMsgs(msgs, 100, 1<<20, 1000)
	if len(got) == 0 || len(got) >= len(msgs) {
		t.Fatalf("按 token 裁剪结果异常：%d", len(got))
	}
}

func TestPruneEngineMsgsCapsBytes(t *testing.T) {
	msgs := []llm.Message{
		{Role: "user", Content: strings.Repeat("a", 500)},
		{Role: "assistant", Content: strings.Repeat("b", 500)},
		{Role: "user", Content: strings.Repeat("c", 500)},
	}
	got := pruneEngineMsgs(msgs, 100, 700, 0)
	if len(got) != 1 || got[0].Content != msgs[2].Content {
		t.Fatalf("按字节裁剪结果异常：%d", len(got))
	}
}
