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
	b.WriteString("\n\n## 项目记忆（历史任务中确认过的事实，可直接采信；与代码现状冲突时以代码为准）\n")
	b.WriteString(digest)
	b.WriteString("\n新增记忆用 memory_save，检索用 memory_search，不要在回答里重复罗列全部记忆。")
	return b.String()
}
