package agent

import (
	"fmt"
	"sort"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/tools"
)

// permTarget 从工具参数里挑出用于规则匹配的目标：
// 命令类看命令行，文件类看解析后的绝对路径，补丁看涉及的文件列表。
func permTarget(name string, args map[string]interface{}, workDir string) string {
	switch name {
	case "run_command":
		return strings.TrimSpace(tools.Get(args, "command"))
	case "apply_patch":
		paths := patchPaths(args)
		if len(paths) > 0 {
			// 优先回报第一个“会跳出工作区”的路径，
			// 让 escapesUnlisted 能拦住：补丁有 N 个文件时
			// 原来的 paths[0] 会让其余 N-1 个文件的越界写直接绕过策略。
			for _, p := range paths {
				if core.EscapesWorkDir(workDir, p) {
					return p
				}
			}
			return paths[0]
		}
		return ""
	case "delete_path":
		return core.ResolvePath(workDir, tools.Get(args, "path"))
	default:
		p := tools.Get(args, "path")
		if p == "" {
			p = tools.Get(args, "file")
		}
		if p == "" {
			return ""
		}
		return core.ResolvePath(workDir, p)
	}
}

// patchPaths 粗略提取补丁里涉及的文件路径，用于目录白名单与审批展示。
func patchPaths(args map[string]interface{}) []string {
	body := tools.Get(args, "patch")
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "*** ") {
			continue
		}
		for _, kw := range []string{"Update File:", "Add File:", "Delete File:"} {
			if strings.HasPrefix(line, "*** "+kw) {
				p := strings.TrimSpace(strings.TrimPrefix(line, "*** "+kw))
				if p != "" && !seen[p] {
					seen[p] = true
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// permRisk 只对命令做风险分级，其它工具的风险由策略模式与写类标记决定。
func permRisk(name string, args map[string]interface{}) core.RiskLevel {
	if name == "run_command" {
		return core.CommandRisk(tools.Get(args, "command"))
	}
	return core.RiskSafe
}

// permDetail 是确认框正文：把"到底要干什么"写清楚，用户才敢点确定。
func permDetail(name string, args map[string]interface{}) string {
	var b strings.Builder
	switch name {
	case "run_command":
		b.WriteString("命令：\n" + tools.Get(args, "command"))
		if cwd := strings.TrimSpace(tools.Get(args, "cwd")); cwd != "" {
			b.WriteString("\n\n目录：" + cwd)
		}
	case "apply_patch":
		paths := patchPaths(args)
		if len(paths) > 0 {
			b.WriteString("涉及文件：\n" + strings.Join(paths, "\n"))
		}
	default:
		if p := tools.Get(args, "path"); p != "" {
			b.WriteString("目标：" + p)
		}
	}
	if b.Len() == 0 {
		b.WriteString("工具：" + name)
	}
	return b.String()
}

// allowedToolList 返回白名单里的工具名，按字母序，便于报错时直接展示。
func (e *Engine) allowedToolList() string {
	names := make([]string, 0, len(e.toolAllow))
	for n := range e.toolAllow {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// setToolAllow 安装工具白名单（自定义子代理使用）。
func (e *Engine) setToolAllow(list []string) {
	if len(list) == 0 {
		e.toolAllow = nil
	} else {
		m := make(map[string]bool, len(list))
		for _, n := range list {
			if n = strings.TrimSpace(n); n != "" {
				m[n] = true
			}
		}
		e.toolAllow = m
	}
	e.toolsMu.Lock()
	e.toolsKey, e.toolsCache, e.extraTools = "", nil, nil
	e.toolsMu.Unlock()
}

// AutoApproveDecision 把旧的"自动执行"开关翻译成策略模式，供配置升级使用。
// 注意：高危命令无论什么模式，Policy.Decide 一律强制确认（RiskDanger 分支）；
// 危险命令不需要第二层"安全模式"开关（旧 safe_mode 字段已删除，配置里的同名键被静默忽略）。
func AutoApproveDecision(autoApprove bool) string {
	if autoApprove {
		return core.PermAuto
	}
	return core.PermAsk
}

// PermSummary 给界面展示单行状态（状态栏用）。
func PermSummary(p *core.Policy) string {
	if p == nil {
		return "策略：默认"
	}
	switch p.Mode {
	case core.PermAuto:
		return "策略：auto"
	case core.PermPlan:
		return "策略：plan"
	default:
		return "策略：ask"
	}
}

// permBanner 拼接确认框标题，避免标题过长被截断后无法判断对象。
func permBanner(title, reason string) string {
	if strings.TrimSpace(reason) == "" {
		return title
	}
	return fmt.Sprintf("%s（%s）", title, reason)
}
