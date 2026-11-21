package agent

import (
	"fmt"
	"sort"
	"strings"

	"win7harness/internal/llm"
	"win7harness/internal/tools"
)

// toolDefsForTurn 按当前模式裁剪工具定义：计划模式只给只读工具，
// 子代理只给白名单，窗口较小的模型自动用 core profile。
// core profile 下可通过 tool_search / tool_load 按需展开扩展工具。
// 结果按注册表版本/模式/已加载扩展工具缓存。
func (e *Engine) toolDefsForTurn() []llm.ToolDef {
	profile := e.effectiveToolProfile()
	rev := tools.Revision()

	e.toolsMu.Lock()
	defer e.toolsMu.Unlock()
	extras := make([]string, 0, len(e.extraTools))
	for n := range e.extraTools {
		extras = append(extras, n)
	}
	sort.Strings(extras)
	key := fmt.Sprintf("%s|%t|%d|%s", profile, e.PlanMode, rev, strings.Join(extras, ","))
	if e.toolsKey == key && e.toolsCache != nil {
		return e.toolsCache
	}

	var defs []llm.ToolDef
	if profile == tools.ProfileCore {
		// core 白名单 + 已按需加载的扩展工具；用 custom allow 而不是再写一套 profile。
		allow := map[string]bool{}
		for _, n := range tools.CoreTools() {
			allow[n] = true
		}
		for _, n := range extras {
			allow[n] = true
		}
		if e.toolAllow != nil {
			for n := range allow {
				if !e.toolAllow[n] {
					delete(allow, n)
				}
			}
		}
		defs = tools.DefsFor(tools.ProfileFull, allow)
	} else {
		var allow map[string]bool
		if e.toolAllow != nil {
			allow = e.toolAllow
		}
		defs = tools.DefsFor(profile, allow)
	}
	e.toolsCache = defs
	e.toolsKey = key
	return defs
}

// LoadTool 把扩展工具加入下一轮的工具定义。计划模式/子代理白名单之外的
// 工具会被拒绝；写工具仍要走原有权限策略，不会因为“加载”而绕过确认。
func (e *Engine) LoadTool(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	t, ok := tools.Find(name)
	if !ok || t.Hidden || t.Core {
		return false
	}
	if e.PlanMode && !tools.IsPlanSafe(name) {
		return false
	}
	if e.toolAllow != nil && !e.toolAllow[name] {
		return false
	}
	e.toolsMu.Lock()
	if e.extraTools == nil {
		e.extraTools = map[string]bool{}
	}
	e.extraTools[name] = true
	e.toolsKey, e.toolsCache = "", nil
	e.toolsMu.Unlock()
	return true
}

// maxOutputTokens 给本轮输出留出合理上限：小窗口模型不能按 8192 输出，
// 否则输入 + 输出直接超过窗口。128k 模型仍用用户配置的值。
func (e *Engine) maxOutputTokens() int {
	max := e.MaxTok
	if max <= 0 {
		max = 4096
	}
	window := e.contextWindow()
	cap := window / 3
	if cap < 512 {
		cap = 512
	}
	if max > cap {
		max = cap
	}
	return max
}

// EffectiveToolProfile 返回当前实际生效的工具 profile（auto 会按窗口解析）。
func (e *Engine) EffectiveToolProfile() string { return e.effectiveToolProfile() }

func (e *Engine) effectiveToolProfile() string {
	profile := e.ToolProfile
	if profile == "" {
		profile = "auto"
	}
	if e.PlanMode {
		return tools.ProfilePlan
	}
	if profile == "auto" {
		// 小窗口本地模型（8k/16k/32k）塞 30+ 个工具 schema 会明显挤占上下文；
		// 128k 模型保持完整工具集，避免能力突然变少。
		if e.ContextWindow > 0 && e.ContextWindow < 64000 {
			return tools.ProfileCore
		}
		return tools.ProfileFull
	}
	return profile
}
