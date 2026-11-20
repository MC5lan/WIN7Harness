//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"win7harness/internal/core"
)

// ExportConv 把一次会话导出成可读的 Markdown 文件，返回落地路径。
// 渲染逻辑在 core.RenderSessionMarkdown（可单测），这里只做数据转换与落盘。
func (a *App) ExportConv(conv *Conv, dir string) (string, error) {
	if conv == nil {
		return "", fmt.Errorf("没有可导出的会话")
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		if wd := strings.TrimSpace(a.cfg.WorkDir); wd != "" {
			dir = filepath.Join(wd, "exports")
		} else {
			dir = filepath.Join(core.LocalDataDir(), "exports")
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	model := strings.TrimSpace(a.convModel())
	if model == "" {
		model = a.cfg.Provider().Model
	}
	meta := core.ExportMeta{
		Title:   conv.Title,
		Model:   model,
		WorkDir: strings.TrimSpace(a.convWorkDir()),
		Tokens:  conv.Tok,
		At:      time.Now(),
	}
	msgs := make([]core.ExportMsg, 0, len(conv.Msgs))
	for _, m := range conv.Msgs {
		em := core.ExportMsg{Role: m.Role, Text: m.Text, Reasoning: m.Reasoning, Err: m.Err, Images: m.Images}
		for _, t := range m.Tools {
			em.Tools = append(em.Tools, core.ExportTool{
				Name:   t.Name,
				Title:  ToolTitle(t.Name),
				Args:   t.Args,
				Brief:  ArgSummary(t.Name, t.Args),
				Output: t.Output,
				Done:   t.Done,
				Err:    t.Err,
			})
		}
		msgs = append(msgs, em)
	}
	body := core.RenderSessionMarkdown(meta, msgs)

	name := core.SafeFileName(conv.Title) + "-" + time.Now().Format("20060102-1504") + ".md"
	path := filepath.Join(dir, name)
	if err := core.WriteFileAtomic(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
