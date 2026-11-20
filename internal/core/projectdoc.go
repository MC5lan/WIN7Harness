package core

import (
	"os"
	"path/filepath"
	"strings"
)

// 项目约定文件名（按优先级）。对标 Claude Code 的 CLAUDE.md、Codex 的 AGENTS.md。
var projectDocNames = []string{"AGENTS.md", "HARNESS.md", "CLAUDE.md", ".harness.md"}

const (
	projectDocMaxBytes = 12 << 10 // 单个文件上限
	projectDocMaxTotal = 20 << 10 // 合并后总上限
	projectDocMaxLevel = 4        // 向上查找层数
)

// ProjectDoc 命中的项目约定文件。
type ProjectDoc struct {
	Path    string
	Content string
}

// LoadProjectDocs 从 workDir 向上逐级查找项目约定文件，由近及远返回。
// 越近的目录优先级越高（模型应优先遵循最近的约定）。
func LoadProjectDocs(workDir string) []ProjectDoc {
	dir, err := filepath.Abs(workDir)
	if err != nil || dir == "" {
		return nil
	}
	var docs []ProjectDoc
	seen := map[string]bool{}
	cur := dir
	for level := 0; level < projectDocMaxLevel && cur != ""; level++ {
		for _, name := range projectDocNames {
			p := filepath.Join(cur, name)
			if seen[p] {
				continue
			}
			seen[p] = true
			st, err := os.Stat(p)
			if err != nil || st.IsDir() || st.Size() == 0 {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if int64(len(data)) > projectDocMaxBytes {
				data = data[:projectDocMaxBytes]
			}
			docs = append(docs, ProjectDoc{Path: p, Content: string(data)})
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return docs
}

// ProjectDocBlock 把项目约定渲染成注入系统提示的文本块。
func ProjectDocBlock(workDir string) string {
	docs := LoadProjectDocs(workDir)
	if len(docs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## 项目约定（来自项目内的约定文件，优先级高于通用偏好）\n")
	total := 0
	for _, d := range docs {
		rel := d.Path
		if r, err := filepath.Rel(workDir, d.Path); err == nil {
			rel = r
		}
		body := strings.TrimSpace(d.Content)
		if total+len(body) > projectDocMaxTotal {
			b.WriteString("\n> 约定文件总量已达上限，后续内容已省略。\n")
			break
		}
		total += len(body)
		b.WriteString("\n### " + rel + "\n" + body + "\n")
	}
	b.WriteString("\n以上约定请在整个任务中遵守；若与用户当次明确指令冲突，以用户当次指令为准。\n")
	return b.String()
}

// ProjectDocSkeleton 生成 /init 用的 AGENTS.md 骨架内容。
func ProjectDocSkeleton(workDir string) string {
	name := filepath.Base(workDir)
	return `# ` + name + ` 项目约定

> 本文件由 WIN7 Harness 生成，Agent 每次会话都会自动读取。
> 只写"必须始终遵守"的稳定约定，保持简短（建议 200 行以内）。

## 项目概览
- 这是什么项目、解决什么问题：

## 技术栈与工具链
- 语言版本：
- 构建命令：
- 测试命令：
- 代码格式化 / lint：

## 目录结构要点
- 

## 编码约定
- 命名风格：
- 错误处理方式：
- 注释与文档要求：
- 禁止事项（如不得引入第三方依赖）：

## 验证要求
- 提交前必须：
- 不接受的做法（如跳过测试、注释掉报错）：
`
}
