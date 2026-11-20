//go:build windows

package app

import (
	"strings"
	"testing"
)

func TestAccessibleItemsExposeChatInputAndButton(t *testing.T) {
	a := &App{
		msgs: []Message{
			{Role: "user", Text: "请检查构建"},
			{Role: "assistant", Text: "构建通过"},
		},
		input:     []rune("下一步"),
		winW:      1200,
		winH:      800,
		streaming: false,
	}
	items := a.AccessibleItems()
	if len(items) != 3 {
		t.Fatalf("items=%d", len(items))
	}
	if items[0].Role == 0 || !strings.Contains(items[0].Value, "构建通过") {
		t.Fatalf("对话项异常：%+v", items[0])
	}
	if items[1].Value != "下一步" {
		t.Fatalf("输入框=%q", items[1].Value)
	}
	if items[2].Name != "发送" || items[2].Role == 0 {
		t.Fatalf("按钮项异常：%+v", items[2])
	}
}
