//go:build windows

package app

import (
	"fmt"
	"strings"
	"time"
	"win7harness/internal/core"
	"win7harness/internal/tools"
)

// 本文件由 commands.go 的 handleSlash 拆分而来（T08），仅移动 case body，未改行为。
// 其余命令：/help /image /tools /recall /resume /model /perm /dir /font /theme /compare。

// slashHelp 处理 /help 及别名。
func (a *App) slashHelp(rest string) bool {
	help := slashHelp + "\n**补充**\n" +
		"- `/selftest` 端到端自检 · `/eval` 评测基线 · `/perf` 运行指标\n" +
		"- `/update` 更新检查与自更新 · `/queue` 任务队列 · `/device` 设备管理\n" +
		"- `/remote` 手机指挥台 · `/loop` 定时任务 · `/watch` 目录守护\n" +
		"- `/notices` 通知中心（页面：通知/自动化足迹/异常报告） · `/font` 字号 · `/theme` 主题 · `/compare` 并列对比\n"
	if custom := customCommandHelp(a.sessionWorkDir()); custom != "" {
		help += "\n**自定义命令**\n" + custom
	}
	a.sysNote(help)
	return true
}

// slashImage 处理 /image 及别名。
func (a *App) slashImage(rest string) bool {
	a.pickImages()
	return true
}

// slashTools 处理 /tools 及别名。
func (a *App) slashTools(rest string) bool {
	arg := strings.ToLower(strings.TrimSpace(rest))
	switch arg {
	case "":
		state := "开启（连续 3 条以上已结束的工具调用合并成一行）"
		if a.cfg.NoFoldTools {
			state = "关闭（每条工具单独一行）"
		}
		profile := a.cfg.ToolProfile
		if profile == "" {
			profile = "auto"
		}
		a.sysNote("**工具卡折叠** 当前：" + state +
			"\n\n**工具定义 profile** 当前：" + profile +
			"\n`/tools profile auto|core|full` 切换；auto 在窗口 <64k 时用 core，节省上下文。\n" +
			"`/tools fold` 开启折叠 · `/tools off` 关闭折叠 · 快捷键 Ctrl+Shift+T\n" +
			"折叠只影响显示，工具输出仍在上下文里，模型照常能看到。")
	case "profile", "auto", "core", "full":
		want := arg
		if want == "profile" {
			a.sysNote("用法：`/tools profile auto|core|full`")
			return true
		}
		if a.streaming {
			a.toast("生成中不能切换工具 profile，先停止", true)
			return true
		}
		a.cfg.ToolProfile = want
		if err := a.cfg.Save(); err != nil {
			a.toast("保存失败："+err.Error(), true)
			return true
		}
		if cv := a.currentConv(); cv != nil {
			a.syncEngineTranscript(cv)
		}
		a.rebuildEngine()
		a.restoreHistory()
		a.sysNote("工具 profile 已切换为 " + want + "，下一轮生效。")
	case "fold", "on", "开", "开启":
		a.cfg.NoFoldTools = false
		a.foldOpen = nil
		_ = a.cfg.Save()
		a.layout()
		a.sysNote("工具卡已按组折叠。单组点一下展开，再点收起。")
	case "off", "unfold", "关", "关闭", "展开":
		a.cfg.NoFoldTools = true
		a.foldOpen = nil
		_ = a.cfg.Save()
		a.layout()
		a.sysNote("工具卡已改为逐条显示。")
	default:
		a.toast("用法：/tools [fold|off] 或 /tools profile auto|core|full", true)
	}
	return true
}

// slashRecall 处理 /recall 及别名。
func (a *App) slashRecall(rest string) bool {
	q := strings.TrimSpace(rest)
	if q == "" {
		a.sysNote("用法：`/recall 关键词`\n\n检索已保存的全部会话（含很久以前的），按会话时间列出命中原话。\n" +
			"想按会话内位置跳转用 `/find`；想让模型自己去翻历史，直接说『查一下上次怎么解决的』，它会调用 history_search。")
		return true
	}
	hits, scanned, err := tools.SearchHistory(core.SessionsDir(), q, 12, 0, time.Now())
	if err != nil {
		a.toast("检索失败："+err.Error(), true)
		return true
	}
	if len(hits) == 0 {
		a.sysNote(fmt.Sprintf("在 %d 个已保存会话里没有找到「%s」。\n可以换更短的关键词，或先用 `/find` 在内存中的会话里再试一次。", scanned, q))
		return true
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "**「%s」命中 %d 处**（扫描 %d 个会话，按时间倒序）\n", q, len(hits), scanned)
	for i, h := range hits {
		title := h.Title
		if title == "" {
			title = "新对话"
		}
		who := "助手答"
		switch h.Role {
		case "user":
			who = "你问"
		case "title":
			who = "标题"
		}
		if strings.HasPrefix(h.Role, "工具·") {
			who = h.Role
		}
		fmt.Fprintf(&sb, "\n%d. 《%s》%s · %s\n   %s\n", i+1, title, h.At.Format("01-02 15:04"), who, h.Around)
	}
	a.sysNote(sb.String())
	return true
}

// slashResume 处理 /resume 及别名。
func (a *App) slashResume(rest string) bool {
	a.resumeLast()
	return true
}

// slashModel 处理 /model 及别名。
func (a *App) slashModel(rest string) bool {
	a.cmdModel(rest)
	return true
}

// slashPerm 处理 /perm 及别名。
func (a *App) slashPerm(rest string) bool {
	a.cmdPerm(rest)
	return true
}

// slashWorkDir 处理 /dir 及别名。
func (a *App) slashWorkDir(rest string) bool {
	a.cmdWorkDir(rest)
	return true
}

// slashFont 处理 /font 及别名。
func (a *App) slashFont(rest string) bool {
	a.cmdFont(rest)
	return true
}

// slashTheme 处理 /theme 及别名。
func (a *App) slashTheme(rest string) bool {
	a.cmdTheme(rest)
	return true
}

// slashCompare 处理 /compare 及别名。
func (a *App) slashCompare(rest string) bool {
	a.cmdCompare(rest)
	return true
}
