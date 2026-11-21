package agent

import (
	"strings"

	"win7harness/internal/core"
)

// memoryBlock 把项目长期记忆注入系统提示。
// 只取最近若干条并限制总长度：记忆会一直长，全量注入早晚把上下文吃光。
func memoryBlock(workDir string) string {
	limit, maxChars := 12, 2400
	if core.LowMemory() {
		limit, maxChars = 6, 1200
	}
	digest := core.MemoryDigest(workDir, limit, maxChars)
	if digest == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## 项目记忆（模型在过去任务中写下的笔记：可能过时或与现状不符，先验证再采信）\n")
	b.WriteString(digest)
	b.WriteString("\n新增记忆用 memory_save，检索用 memory_search；与代码、命令结果冲突时以现状为准，不要在回答里重复罗列全部记忆。")
	return b.String()
}
