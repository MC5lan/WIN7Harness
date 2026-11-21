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
	// 带参数（/skills list）仍走文字版，方便脚本化与日志留痕。
	if strings.TrimSpace(rest) == "" {
		a.page = PageExt
		a.extTab = extTabSkills
		return true
	}
	list := skills.Scan(a.cfg.WorkDir)
	if len(list) == 0 {
		a.sysNote(fmt.Sprintf("暂无技能。把流程文档放到下面任一位置即可被自动发现：\n%s\\<技能名>\\SKILL.md\n%s\\<技能名>.md",
			skills.ProjectDir(a.cfg.WorkDir), skills.GlobalDir()))
		return true
	}
	var b strings.Builder
	fmt.Fprintf(&b, "可用技能 %d 个：\n", len(list))
	for _, sk := range list {
		scope := "项目"
		if sk.Scope == "global" {
			scope = "全局"
		}
		fmt.Fprintf(&b, "- %s（%s）%s\n", sk.Name, scope, sk.Desc)
	}
	b.WriteString("用 /skill 名称 加载正文，模型侧会自动用 skill_use 加载。")
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
		a.sysNote("用法：/skill 技能名（先用 /skills 看清单）")
		return true
	}
	sk, ok := skills.Find(a.cfg.WorkDir, name)
	if !ok {
		a.sysNote("没有名为“" + name + "”的技能，先用 /skills 查看清单。")
		return true
	}
	a.sysNote(fmt.Sprintf("技能 %s 已就绪（%s）。直接说要做的事，我会按该流程执行。", sk.Name, sk.Path))
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
