package agent

import (
	"strings"

	"win7harness/internal/llm"
	"win7harness/internal/tools"
)

// imageMarker 是 read_image 工具输出里的图片载荷标记。
// 工具只回文字，真正发给模型的图片在这里被抽出、转成多模态消息，
// 这样工具结果在历史里仍是纯文本，压缩与导出都不受影响。
const imageMarker = "[[img:"
const imageMarkerEnd = "]]"

// syncBoardFromTool 把工具结果同步到任务侧栏。
func (e *Engine) syncBoardFromTool(name string, args map[string]interface{}, out string) {
	b := e.Board()
	switch name {
	case "todo_write":
		raw, _ := args["items"].([]interface{})
		var items []TodoItem
		for _, it := range raw {
			m, ok := it.(map[string]interface{})
			if !ok {
				continue
			}
			st, _ := m["status"].(string)
			if st == "" {
				st = "pending"
			}
			items = append(items, TodoItem{Text: tools.Get(m, "text"), Status: st})
		}
		if len(items) > 0 {
			b.SetTodos(items)
		}
	case "task":
		// 批量派发期间显示进度，结束后清空。
		b.SetPhase("子任务派发中")
	}
}

// collectImages 从工具输出里抽出 read_image 带回的图片 data URL。
func (e *Engine) collectImages(name string, out string) {
	if name != "read_image" || !strings.Contains(out, imageMarker) {
		return
	}
	rest := out
	for {
		i := strings.Index(rest, imageMarker)
		if i < 0 {
			break
		}
		j := strings.Index(rest[i:], imageMarkerEnd)
		if j < 0 {
			break
		}
		url := rest[i+len(imageMarker) : i+j]
		rest = rest[i+j+len(imageMarkerEnd):]
		if strings.HasPrefix(url, "data:image/") {
			e.imgMu.Lock()
			e.pendingImgs = append(e.pendingImgs, url)
			e.imgMu.Unlock()
		}
	}
}

// flushPendingImages 把收集到的图片作为一条用户消息喂给模型（content 为多模态数组）。
// 图片只能挂在 user 消息上，tool 消息只带文本，因此单独补一条。
func (e *Engine) flushPendingImages() {
	e.imgMu.Lock()
	imgs := e.pendingImgs
	e.pendingImgs = nil
	e.imgMu.Unlock()
	if len(imgs) == 0 {
		return
	}
	parts := make([]llm.Part, 0, len(imgs)+1)
	parts = append(parts, llm.TextPart("（以下是 read_image 读取的图片，请直接看图回答）"))
	for _, u := range imgs {
		parts = append(parts, llm.ImagePart(u))
	}
	e.mu.Lock()
	e.Messages = append(e.Messages, llm.Message{Role: "user", Parts: parts})
	e.mu.Unlock()
}

// stripImageMarkers 去掉工具输出里的图片载荷，只留可读描述。
// 载荷体积巨大，留在工具卡片或上下文里既费内存也没意义。
func stripImageMarkers(out string) string {
	if !strings.Contains(out, imageMarker) {
		return out
	}
	var b strings.Builder
	rest := out
	for {
		i := strings.Index(rest, imageMarker)
		if i < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:i])
		j := strings.Index(rest[i:], imageMarkerEnd)
		if j < 0 {
			b.WriteString(rest[i:])
			break
		}
		rest = rest[i+j+len(imageMarkerEnd):]
	}
	return strings.TrimRight(b.String(), "\n")
}
