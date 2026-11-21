package llm

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// Part 是多模态消息里的一段内容（OpenAI Chat Completions 的 content 数组元素）。
// 只有纯文本的消息仍然直接发字符串，保证对老服务端、老模型的完全兼容。
type Part struct {
	Type     string    `json:"type"` // text | image_url
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
	// CacheControl 只对支持 Anthropic 风格提示缓存的网关有效；
	// 普通 OpenAI 兼容端点不会收到该字段（见 Message.Cache）。
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// CacheControl 是 Anthropic 风格提示缓存的标记。
type CacheControl struct {
	Type string `json:"type"` // ephemeral
}

// ImageURL 图片地址，支持 data URL（data:image/png;base64,...）与 http(s) 链接。
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // low | high | auto
}

// TextPart 构造一段文本。
func TextPart(s string) Part { return Part{Type: "text", Text: s} }

// ImagePart 用 data URL 构造一张图片段。
func ImagePart(dataURL string) Part {
	return Part{Type: "image_url", ImageURL: &ImageURL{URL: dataURL}}
}

// ImageDataURL 把图片字节编码成 data URL。超过 12MB 的图直接拒绝（base64 会再膨胀 33%）：
// 32 位进程里几个大图直接把地址空间吃光。返回空串表示被拒绝。
func ImageDataURL(mime string, data []byte) string {
	const maxImageURLBytes = 12 << 20
	if len(data) > maxImageURLBytes {
		return ""
	}
	if mime == "" {
		mime = "image/png"
	}
	var b strings.Builder
	b.Grow(len(data)*4/3 + 32)
	b.WriteString("data:")
	b.WriteString(mime)
	b.WriteString(";base64,")
	enc := base64.StdEncoding.EncodeToString(data)
	b.WriteString(enc)
	return b.String()
}

// wireMessage 是实际发出的消息结构。Content 用 any 承载字符串或数组。
type wireMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"`
	Reasoning  string     `json:"reasoning_content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// MarshalJSON 让带图的消息序列化成 content 数组，不带图的仍然输出字符串。
// 这样老服务端/纯文本模型看到的请求体与 v1.7 完全一致，不会因为多模态改动而回归。
func (m Message) MarshalJSON() ([]byte, error) {
	w := wireMessage{
		Role:       m.Role,
		Reasoning:  m.Reasoning,
		Name:       m.Name,
		ToolCalls:  m.ToolCalls,
		ToolCallID: m.ToolCallID,
	}
	if len(m.Parts) > 0 {
		parts := make([]Part, 0, len(m.Parts)+1)
		// 文本统一放最前面，部分服务端对顺序敏感
		if m.Content != "" {
			parts = append(parts, TextPart(m.Content))
		}
		parts = append(parts, m.Parts...)
		w.Content = parts
	} else if m.Content != "" {
		if m.Cache {
			// Anthropic 风格缓存标记：content 必须是 block 数组，不能是纯字符串。
			w.Content = []Part{{
				Type: "text", Text: m.Content,
				CacheControl: &CacheControl{Type: "ephemeral"},
			}}
		} else {
			w.Content = m.Content
		}
	}
	return json.Marshal(w)
}

// HasImages 判断消息是否带图片。
func (m Message) HasImages() bool {
	for _, p := range m.Parts {
		if p.Type == "image_url" && p.ImageURL != nil && p.ImageURL.URL != "" {
			return true
		}
	}
	return false
}

// MessageText 返回消息的纯文本（不含图片），用于日志与剪贴板等场景。
func (m Message) MessageText() string {
	if m.Content != "" {
		return m.Content
	}
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}
