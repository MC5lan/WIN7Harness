package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageJSONPlainTextUnchanged(t *testing.T) {
	m := Message{Role: "user", Content: "你好"}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"content":"你好"`) {
		t.Fatalf("纯文本消息 content 仍是字符串，实际：%s", s)
	}
	if strings.Contains(s, `"parts"`) {
		t.Fatalf("不应出现 parts 字段：%s", s)
	}
}

func TestMessageJSONWithImage(t *testing.T) {
	m := Message{
		Role:    "user",
		Content: "这张图是什么",
		Parts:   []Part{ImagePart("data:image/png;base64,AAA")},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Role    string `json:"role"`
		Content []Part `json:"content"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("content 应为数组，实际：%s", string(b))
	}
	if len(got.Content) != 2 {
		t.Fatalf("期望 2 段（文本+图片），实际 %d 段：%s", len(got.Content), string(b))
	}
	if got.Content[0].Type != "text" || got.Content[0].Text != "这张图是什么" {
		t.Fatalf("第一段应为文本：%+v", got.Content[0])
	}
	if got.Content[1].Type != "image_url" || got.Content[1].ImageURL == nil {
		t.Fatalf("第二段应为图片：%+v", got.Content[1])
	}
	if got.Content[1].ImageURL.URL != "data:image/png;base64,AAA" {
		t.Fatalf("图片 URL 不符：%s", got.Content[1].ImageURL.URL)
	}
}

func TestMessageJSONImageOnly(t *testing.T) {
	// 只有图片、没有文本时不能生成空的 text 段
	m := Message{Role: "user", Parts: []Part{ImagePart("data:image/png;base64,BBB")}}
	b, _ := json.Marshal(m)
	if strings.Contains(string(b), `"type":"text"`) {
		t.Fatalf("不应有空文本段：%s", string(b))
	}
	if !m.HasImages() {
		t.Fatal("HasImages 应为 true")
	}
}

func TestMessageToolFieldsPreserved(t *testing.T) {
	m := Message{
		Role: "assistant",
		ToolCalls: []ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: FunctionCall{Name: "read_file", Arguments: `{"path":"a"}`},
		}},
	}
	b, _ := json.Marshal(m)
	s := string(b)
	if !strings.Contains(s, `"tool_calls"`) || !strings.Contains(s, `"read_file"`) {
		t.Fatalf("工具调用字段丢失：%s", s)
	}
	if !strings.Contains(s, `"role":"assistant"`) {
		t.Fatalf("role 丢失：%s", s)
	}
}

func TestMessageJSONOmitsReasoning(t *testing.T) {
	m := Message{Role: "assistant", Content: "结论", Reasoning: "很长的思考过程"}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "reasoning_content") || strings.Contains(string(b), "思考过程") {
		t.Fatalf("reasoning_content 不应再发回模型：%s", string(b))
	}
}

func TestMessageJSONPromptCache(t *testing.T) {
	m := Message{Role: "system", Content: "稳定前缀", Cache: true}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Content []Part `json:"content"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("cache 消息 content 应为数组：%s", string(b))
	}
	if len(got.Content) != 1 || got.Content[0].CacheControl == nil || got.Content[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("cache_control 未写入：%s", string(b))
	}
	// 未打标的消息必须保持字符串，不能影响普通端点。
	plain, _ := json.Marshal(Message{Role: "system", Content: "普通"})
	if !strings.Contains(string(plain), `"content":"普通"`) {
		t.Fatalf("普通消息被改成数组：%s", string(plain))
	}
}

func TestShouldCacheSystem(t *testing.T) {
	c := &Client{BaseURL: "https://openrouter.ai/api/v1", Model: "anthropic/claude-3.5-sonnet", PromptCache: "auto"}
	if !c.shouldCacheSystem() {
		t.Fatal("OpenRouter + Claude 应自动启用")
	}
	c.Model = "deepseek-chat"
	if c.shouldCacheSystem() {
		t.Fatal("未知模型不应自动启用")
	}
	c.PromptCache = "anthropic"
	if !c.shouldCacheSystem() {
		t.Fatal("显式 anthropic 应启用")
	}
	c.PromptCache = "off"
	if c.shouldCacheSystem() {
		t.Fatal("off 应关闭")
	}
}

func TestImageDataURL(t *testing.T) {
	u := ImageDataURL("", []byte{1, 2, 3})
	if !strings.HasPrefix(u, "data:image/png;base64,") {
		t.Fatalf("data URL 前缀不符：%s", u)
	}
}

func TestMessageText(t *testing.T) {
	m := Message{Content: "原文"}
	if m.MessageText() != "原文" {
		t.Fatal("MessageText 应返回 Content")
	}
	m2 := Message{Parts: []Part{TextPart("来自 Parts")}}
	if m2.MessageText() != "来自 Parts" {
		t.Fatal("MessageText 应回落到 Parts 文本")
	}
}
