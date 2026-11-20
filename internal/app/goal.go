package app

import (
	"fmt"
	"strings"
)

// /goal 命令：按"拆解 → 逐步执行 → 整体验证"的收敛循环推进目标。
// 不带参数时回看上一次目标的结果。
func (a *App) cmdGoal(rest string) {
	if a.eng == nil {
		a.sysNote("模型还没配置好，暂时无法执行目标收敛。")
		return
	}
	goal := strings.TrimSpace(rest)
	if goal == "" {
		rep := a.eng.LastGoal()
		if rep == nil {
			a.sysNote("目标收敛循环：把一个较大的目标拆成若干步，逐步执行并整体验收，不达标自动再来一轮。\n\n" +
				"用法：`/goal 目标描述`\n例：`/goal 把 internal/core 的日志输出统一到 core.Logf 并保证全量测试通过`\n\n" +
				"停止：生成中按 Esc / 点停止按钮。")
			return
		}
		var b strings.Builder
		b.WriteString("上次目标：" + rep.Goal + "\n" + rep.Summary() + "\n")
		if rep.Detail != "" {
			b.WriteString("验证结论：" + truncateText(rep.Detail, 300) + "\n")
		}
		for i, s := range rep.Steps {
			mark := "○"
			if s.Done {
				mark = "✓"
			}
			fmt.Fprintf(&b, "%s %d. %s", mark, i+1, s.Desc)
			if s.Attempts > 1 {
				fmt.Fprintf(&b, "（尝试 %d 次）", s.Attempts)
			}
			b.WriteString("\n")
		}
		a.sysNote(b.String())
		return
	}
	if a.streaming {
		a.toast("当前还有任务在跑，等它结束再启动目标收敛", true)
		return
	}
	if !a.eng.RunGoalAsync(goal) {
		a.toast("启动失败：引擎正忙", true)
		return
	}
	a.streaming = true
	a.autoScroll = true
	a.statusMsg = "目标收敛启动中…"
	a.msgs = append(a.msgs, Message{Role: "user", Text: "/goal " + goal})
	a.sysNote("**目标收敛已启动**\n\n目标：" + goal +
		"\n\n流程：拆成若干步 → 逐步执行 → 整体跑构建与测试验收；不通过自动再来一轮。" +
		"\n进度会实时显示在右侧任务栏，随时可按 Esc 停止。")
}

// truncateText 按字符截断长文本。
func truncateText(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "…"
}
