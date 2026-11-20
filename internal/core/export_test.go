package core

import (
	"strings"
	"testing"
	"time"
)

func TestRenderSessionMarkdown(t *testing.T) {
	at := time.Date(2026, 11, 14, 10, 30, 0, 0, time.Local)
	meta := ExportMeta{Title: "重构设置页", Model: "gpt-4o", WorkDir: `D:\proj`, Tokens: 1234, At: at}
	msgs := []ExportMsg{
		{Role: "user", Text: "帮我把设置页改成可滚动"},
		{
			Role:      "assistant",
			Reasoning: "先看现有布局",
			Text:      "改好了。",
			Tools: []ExportTool{{
				Name:   "edit_file",
				Title:  "修改文件",
				Brief:  "internal/app/pages.go",
				Output: "ok",
				Done:   true,
			}},
		},
	}
	got := RenderSessionMarkdown(meta, msgs)

	for _, want := range []string{
		"# 重构设置页",
		"- 模型：gpt-4o",
		"- 工作目录：D:\\proj",
		"- 累计 token：1234",
		"## 我",
		"帮我把设置页改成可滚动",
		"<details><summary>思考过程</summary>",
		"> **工具** `edit_file`（修改文件）完成",
		"> 参数：internal/app/pages.go",
		"改好了。",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("导出内容缺少 %q\n---\n%s", want, got)
		}
	}
	// 工具输出必须包在代码块里，避免被 Markdown 渲染吞掉
	if !strings.Contains(got, "```\nok\n```") {
		t.Fatalf("工具输出未放入代码块: %s", got)
	}
}

func TestRenderSessionMarkdownTruncates(t *testing.T) {
	var long strings.Builder
	for i := 0; i < 60; i++ {
		long.WriteString("out line\n")
	}
	msgs := []ExportMsg{{
		Role: "assistant",
		Tools: []ExportTool{{
			Name:   "run_command",
			Output: long.String(),
			Err:    true,
		}},
	}}
	got := RenderSessionMarkdown(ExportMeta{}, msgs)

	if !strings.Contains(got, "（失败）") {
		t.Fatalf("失败状态未体现: %s", got)
	}
	if !strings.Contains(got, "另有 20 行输出未导出") {
		t.Fatalf("超长输出应被截断为 40 行: %s", got)
	}
	if strings.Count(got, "out line") != 40 {
		t.Fatalf("应保留 40 行，实际 %d 行", strings.Count(got, "out line"))
	}
	// 标题为空时给默认标题，不能导出成空文档
	if !strings.Contains(got, "# 未命名会话") {
		t.Fatalf("空标题应回退为默认标题: %s", got)
	}
}

func TestSafeFileName(t *testing.T) {
	cases := map[string]string{
		`a\b:c*d?e"f<g>h|i`: "a-b-cdefgh-i",
		"   ":               "session",
		"":                  "session",
	}
	for in, want := range cases {
		if got := SafeFileName(in); got != want {
			t.Fatalf("SafeFileName(%q) = %q, want %q", in, got, want)
		}
	}
	// 超长标题必须被限长，避免路径过长写入失败
	long := strings.Repeat("长", 100)
	if n := len([]rune(SafeFileName(long))); n != 40 {
		t.Fatalf("超长标题应截断到 40 字，实际 %d", n)
	}
}
