//go:build windows

package app

import (
	"fmt"
	"strings"
	"win7harness/internal/commands"
	"win7harness/internal/core"
	"win7harness/internal/skills"
	"win7harness/internal/tools"
)

// 本文件由 commands.go 的 handleSlash 拆分而来（T08），仅移动 case body，未改行为。
// 智能体与扩展：/plan /goal /skills /skill /commands /hooks /memory /verify /fork /agents /loop /watch /queue。

// slashPlan 处理 /plan 及别名。
func (a *App) slashPlan(rest string) bool {
	if a.eng == nil {
		return true
	}
	a.eng.PlanMode = !a.eng.PlanMode
	if a.eng.PlanMode {
		a.sysNote("已进入计划模式：我只做调研与方案设计，不写文件、不执行命令。\n方案确认后输入 /plan 退出并开始实施。")
	} else {
		a.sysNote("已退出计划模式：可以正常读写文件与执行命令。")
	}
	return true
}

// slashGoal 处理 /goal 及别名。
func (a *App) slashGoal(rest string) bool {
	a.cmdGoal(rest)
	return true
}

// slashSkills 处理 /skills 及别名。
func (a *App) slashSkills(rest string) bool {
	// 无参打开扩展页：清单做成列表比在聊天里贴一段更能用（能打开正文文件）。
	// /skills off 名称 / on 名称 管理禁用列表；/skills list 仍走文字版。
	if strings.TrimSpace(rest) == "" {
		a.page = PageExt
		a.extTab = extTabSkills
		return true
	}
	fields := strings.Fields(rest)
	if len(fields) >= 2 && (strings.EqualFold(fields[0], "off") || strings.EqualFold(fields[0], "on")) {
		name := strings.TrimSpace(strings.Join(fields[1:], " "))
		enable := strings.EqualFold(fields[0], "on")
		if a.cfg == nil || name == "" {
			return true
		}
		var next []string
		found := false
		for _, n := range a.cfg.DisabledSkills {
			if strings.EqualFold(strings.TrimSpace(n), name) {
				found = true
				if !enable {
					next = append(next, n)
				}
				continue
			}
			next = append(next, n)
		}
		if !enable && !found {
			next = append(next, name)
		}
		a.cfg.DisabledSkills = next
		if err := a.cfg.Save(); err != nil {
			a.toast("保存失败："+err.Error(), true)
			return true
		}
		if !enable && a.eng != nil && strings.EqualFold(a.eng.ActiveSkill(), name) {
			a.eng.ClearActiveSkill()
		}
		a.uiDataDrop()
		if enable {
			a.sysNote("已启用技能：" + name)
		} else {
			a.sysNote("已禁用技能：" + name + "（项目/全局同名技能也会一起隐藏；再用 `/skills on 名称` 恢复）")
		}
		return true
	}
	wd := a.convWorkDir()
	list := skills.ScanMeta(wd)
	if len(list) == 0 {
		a.sysNote(fmt.Sprintf("暂无技能。把流程文档放到下面任一位置即可被自动发现：\n%s\\<技能名>\\SKILL.md\n%s\\<技能名>.md",
			skills.ProjectDir(wd), skills.GlobalDir()))
		return true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "可用技能 %d 个：\n", len(list))
	for _, sk := range list {
		scope := "项目"
		switch sk.Scope {
		case "global":
			scope = "全局"
		case "builtin":
			scope = "内置"
		}
		fmt.Fprintf(&b, "- %s（%s）%s\n", sk.Name, scope, sk.Desc)
	}
	b.WriteString("用 /skill 名称 激活技能；激活后下一轮会把正文注入上下文。模型侧也会按需用 skill_use 加载。")
	a.sysNote(b.String())
	return true
}

// slashCommands 处理 /commands 及别名。
func (a *App) slashCommands(rest string) bool {
	list := commands.Scan(a.sessionWorkDir())
	var b strings.Builder
	if len(list) == 0 {
		fmt.Fprintf(&b, "暂无自定义命令。把 Markdown 放到下面任一位置即可被 /命令名 调用：\n%s\\<名字>.md\n%s\\<名字>.md\n",
			commands.ProjectDir(a.sessionWorkDir()), commands.GlobalDir())
	} else {
		fmt.Fprintf(&b, "自定义命令 %d 个（展开到输入框，可编辑后再发送）：\n", len(list))
		for _, c := range list {
			scope := "项目"
			if c.Scope == "global" {
				scope = "全局"
			}
			fmt.Fprintf(&b, "- /%s（%s）%s\n", c.Name, scope, c.Desc)
		}
	}
	a.sysNote(b.String())
	return true
}

// slashSkill 处理 /skill 及别名。
func (a *App) slashSkill(rest string) bool {
	name := strings.TrimSpace(rest)
	if name == "" {
		a.sysNote("用法：/skill 技能名（先用 /skills 看清单）；/skill off 取消当前技能")
		return true
	}
	if a.eng == nil {
		a.sysNote("模型还没配置好，技能需要在会话里激活。")
		return true
	}
	if strings.EqualFold(name, "off") || strings.EqualFold(name, "clear") {
		a.eng.ClearActiveSkill()
		a.sysNote("已取消当前激活技能。")
		return true
	}
	used, err := a.eng.ActivateSkill(name)
	if err != nil {
		a.sysNote(err.Error() + "，先用 /skills 查看清单。")
		return true
	}
	a.sysNote(fmt.Sprintf("技能 %s 已激活。下一轮会把正文注入上下文；不需要时说 /skill off。", used))
	a.toast("已激活技能："+used, false)
	return true
}

// slashInstructions 维护用户全局指令（注入系统提示的用户段，优先于项目约定）。
func (a *App) slashInstructions(rest string) bool {
	rest = strings.TrimSpace(rest)
	if a.cfg == nil {
		return true
	}
	if a.streaming {
		a.toast("生成中不能改全局指令，先停止或等本轮结束", true)
		return true
	}
	switch strings.ToLower(rest) {
	case "":
		if strings.TrimSpace(a.cfg.SystemPrompt) == "" {
			a.sysNote("还没有用户全局指令。用法：`/instructions 回答用中文，先给结论，不要寒暄`；清除：`/instructions clear`。\n它会注入系统提示的用户段，优先于项目约定，但不能覆盖安全边界。")
		} else {
			a.sysNote("当前用户全局指令：\n\n" + a.cfg.SystemPrompt + "\n\n修改：`/instructions 新内容`；清除：`/instructions clear`。")
		}
		return true
	case "clear", "off", "reset":
		a.cfg.SystemPrompt = ""
	default:
		if len([]rune(rest)) > 4000 {
			rest = string([]rune(rest)[:4000]) + "…"
		}
		a.cfg.SystemPrompt = rest
	}
	if err := a.cfg.Save(); err != nil {
		a.toast("保存失败："+err.Error(), true)
		return true
	}
	// 引擎按配置重建；先把结构化轨迹落进会话，再按新配置恢复，避免丢上下文。
	if cv := a.currentConv(); cv != nil {
		a.syncEngineTranscript(cv)
	}
	a.rebuildEngine()
	a.restoreHistory()
	a.sysNote("用户全局指令已更新，下一轮生效。")
	return true
}

// slashHooks 处理 /hooks 及别名。
func (a *App) slashHooks(rest string) bool {
	lines := tools.HooksSummary()
	if len(lines) == 0 {
		a.sysNote("尚未配置钩子。可在 config.json 的 hooks 数组里添加，例如：\n" +
			`  {"event":"post_tool","match":"write_file,edit_file","command":"gofmt -w %f"}` +
			"\n支持事件：pre_tool（非零退出码可阻止工具执行）、post_tool、turn_end；match 支持 * 通配与逗号分隔。")
	} else {
		a.sysNote("已配置钩子（按字母序）：\n" + strings.Join(lines, "\n"))
	}
	return true
}

// slashVerify 处理 /verify 及别名。
func (a *App) slashVerify(rest string) bool {
	a.cmdVerify(rest)
	return true
}

// slashMemory 处理 /memory 及别名。
func (a *App) slashMemory(rest string) bool {
	f := strings.Fields(strings.ToLower(rest))
	if len(f) > 0 {
		on := f[0] == "on" || f[0] == "low" || f[0] == "1"
		off := f[0] == "off" || f[0] == "normal" || f[0] == "0"
		if !on && !off {
			a.toast("用法：/memory · /memory on · /memory off", true)
			return true
		}
		core.SetLowMemory(on)
		if a.cfg != nil {
			a.cfg.LowMemory = on
			if err := a.cfg.Save(); err != nil {
				a.toast("保存失败："+err.Error(), true)
			}
		}
		tip := "低内存模式已开启，新的调用立即按新阈值执行；代码索引规模要 /index 重建才会回落。"
		if !on {
			tip = "已回到标准档位，新的调用立即按标准阈值执行；索引上限需 /index 重建才会放开。"
		}
		a.sysNote(tip + "\n\n" + core.MemProfile().Describe())
		return true
	}
	a.sysNote(fmt.Sprintf("内存档位说明（当前：%s）\n%s\n\n切换：/memory on 省内存 · /memory off 回到标准",
		core.MemProfile().ModeName(), core.MemProfile().Describe()))
	return true
}

// slashFork 处理 /fork 及别名。
func (a *App) slashFork(rest string) bool {
	a.cmdFork(rest)
	return true
}

// slashAgents 处理 /agents 及别名。
func (a *App) slashAgents(rest string) bool {
	a.cmdAgents(rest)
	return true
}

// slashLoop 处理 /loop 及别名。
func (a *App) slashLoop(rest string) bool {
	a.cmdLoop(rest)
	return true
}

// slashWatch 处理 /watch 及别名。
func (a *App) slashWatch(rest string) bool {
	a.cmdWatch(rest)
	return true
}

// slashQueue 处理 /queue 及别名。
func (a *App) slashQueue(rest string) bool {
	a.cmdQueue(rest)
	return true
}
