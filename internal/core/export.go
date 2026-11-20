package core

import (
	"fmt"
	"strings"
	"time"
)

// ExportTool 导出时一个工具调用的最小信息集。
type ExportTool struct {
	Name   string
	Title  string // 中文短名，由界面层提供
	Args   string // 原始参数 JSON
	Brief  string // 参数摘要
	Output string
	Done   bool
	Err    bool
}

// ExportMsg 导出时一条消息的最小信息集。
type ExportMsg struct {
	Role      string
	Text      string
	Reasoning string
	Err       string
	Tools     []ExportTool
	// Images 用户消息附带的图片路径（绝对路径），导出为 Markdown 图片链接。
	Images []string
}

// ExportMeta 导出文件的头部元信息。
type ExportMeta struct {
	Title   string
	Model   string
	WorkDir string
	Tokens  int
	At      time.Time
}

// RenderSessionMarkdown 把会话渲染成 Markdown 文本。
// 与界面无关，便于单测覆盖格式（工具输出截断、思考折叠、空会话等）。
func RenderSessionMarkdown(meta ExportMeta, msgs []ExportMsg) string {
	var b strings.Builder
	title := strings.TrimSpace(meta.Title)
	if title == "" {
		title = "未命名会话"
	}
	fmt.Fprintf(&b, "# %s\n\n", title)
	at := meta.At
	if at.IsZero() {
		at = time.Now()
	}
	fmt.Fprintf(&b, "- 导出时间：%s\n", at.Format("2006-01-02 15:04"))
	if meta.Model != "" {
		fmt.Fprintf(&b, "- 模型：%s\n", meta.Model)
	}
	if meta.WorkDir != "" {
		fmt.Fprintf(&b, "- 工作目录：%s\n", meta.WorkDir)
	}
	if meta.Tokens > 0 {
		fmt.Fprintf(&b, "- 累计 token：%d\n", meta.Tokens)
	}
	b.WriteString("\n---\n\n")

	for _, m := range msgs {
		if m.Role == "user" {
			b.WriteString("## 我\n\n")
			b.WriteString(strings.TrimRight(m.Text, "\n"))
			b.WriteString("\n\n")
			for _, img := range m.Images {
				if strings.TrimSpace(img) == "" {
					continue
				}
				// 用正斜杠，保证在其他 Markdown 编辑器里也能点开
				p := strings.ReplaceAll(img, "\\", "/")
				fmt.Fprintf(&b, "![图片](%s)\n\n", p)
			}
			continue
		}
		b.WriteString("## 助手\n\n")
		if r := strings.TrimSpace(m.Reasoning); r != "" {
			if len([]rune(r)) > 1200 {
				r = string([]rune(r)[:1200]) + "\n\n…（思考过程过长已截断）"
			}
			b.WriteString("<details><summary>思考过程</summary>\n\n")
			b.WriteString(strings.TrimRight(r, "\n"))
			b.WriteString("\n\n</details>\n\n")
		}
		for _, t := range m.Tools {
			state := "完成"
			if !t.Done {
				state = "未完成"
			}
			if t.Err {
				state = "失败"
			}
			name := t.Name
			if t.Title != "" && t.Title != t.Name {
				fmt.Fprintf(&b, "> **工具** `%s`（%s）%s\n", name, t.Title, state)
			} else {
				fmt.Fprintf(&b, "> **工具** `%s`（%s）\n", name, state)
			}
			if s := strings.TrimSpace(t.Brief); s != "" {
				fmt.Fprintf(&b, "> 参数：%s\n", s)
			}
			if out := strings.TrimSpace(t.Output); out != "" {
				lines := strings.Split(out, "\n")
				if len(lines) > 40 {
					out = strings.Join(lines[:40], "\n") + fmt.Sprintf("\n…（另有 %d 行输出未导出）", len(lines)-40)
				}
				b.WriteString("\n```\n")
				b.WriteString(out)
				b.WriteString("\n```\n")
			}
			b.WriteString("\n")
		}
		if t := strings.TrimRight(m.Text, "\n"); t != "" {
			b.WriteString(t)
			b.WriteString("\n\n")
		}
		if m.Err != "" {
			b.WriteString("> 出错：" + m.Err + "\n\n")
		}
	}
	return b.String()
}

// SafeFileName 去掉 Windows 文件名非法字符并限长，避免导出失败。
func SafeFileName(s string) string {
	s = strings.TrimSpace(s)
	repl := strings.NewReplacer("\\", "-", "/", "-", ":", "-", "*", "",
		"?", "", "\"", "", "<", "", ">", "", "|", "-", "\n", " ", "\r", " ")
	s = repl.Replace(s)
	if len([]rune(s)) > 40 {
		s = string([]rune(s)[:40])
	}
	if s == "" {
		s = "session"
	}
	return s
}
