package core

import "testing"

func TestSuggestModelCapabilities(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"gemini-2.5-pro", 1_000_000},
		{"claude-sonnet-4", 200_000},
		{"deepseek-flash", 128_000},
		{"qwen3.8-max", 131_072},
		{"unknown-model", 0},
	}
	for _, c := range cases {
		got := SuggestModelCapabilities("", c.model)
		if got.ContextWindow != c.want {
			t.Fatalf("%s 窗口建议 = %d，期望 %d", c.model, got.ContextWindow, c.want)
		}
	}
	local := SuggestModelCapabilities("http://127.0.0.1:11434/v1", "qwen3.8:27b")
	if local.ContextWindow != 131_072 {
		// 模型名优先于端点：qwen 规则先命中，这是有意为之。
		t.Fatalf("qwen 本地建议异常：%+v", local)
	}
}
