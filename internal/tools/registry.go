// Package tools 实现 Harness 的本地工具集（读写文件、检索、执行命令等）。
// 工具定义同时用于 LLM function calling 与界面展示。
package tools

import (
	"context"
	"encoding/json"
	"sync"

	"win7harness/internal/core"
	"win7harness/internal/llm"
)

// Context 工具运行上下文。
type Context struct {
	WorkDir string
	// Ctx 是可取消的任务上下文（可空）。设置后，
	// runShell 在外部取消时会终止整棵进程树，而不是让构建/测试在后台空转——
	// "用户点了停止"意味着杀进程，而不是停止等待。
	Ctx context.Context
	// Confirm 在执行高风险操作前询问用户，返回 false 表示拒绝。
	Confirm func(title, detail string) bool
	// ConfirmDiff 是带行号与 hunk 边界的差分确认，返回被接受的 hunk 下标。
	// nil 表示拒绝；空切片表示全部拒绝；长度等于传入 hunk 数表示全部接受。
	// 未注入时所有差分确认退化为 Confirm(title, 纯文本 diff)。
	ConfirmDiff func(title, path string, hunks []core.DiffHunk, allowPartial bool) []int
	// Progress 上报一行进度文本（显示在界面工具卡片里）。
	Progress func(string)
	// Spawn 派发子 Agent，由 agent 包注入；子 Agent 上下文里为 nil。
	Spawn func(prompt, desc string, readOnly bool) (string, error)
	// SpawnNamed 用工作区里定义好的自定义子代理（.harness/agents/*.md）派生任务。
	SpawnNamed func(def *core.AgentDef, prompt, desc string, readOnly bool) (string, error)
	// SpawnBatch 在隔离副本里并行执行多个写子任务并合并结果；
	// 为 nil 时 task 工具对写子任务退化为串行。
	SpawnBatch func(tasks []BatchTask) (string, error)
	// MaxSubTasks 一次批量派发的子任务上限，0 表示用默认值。
	MaxSubTasks int
	// HistoryDir 历史会话目录，history_search 用它做跨会话检索；空则回落到 core.SessionsDir()。
	HistoryDir string
	// SubTaskMaxChars 单个子任务返回结论的字数上限，0 表示用默认值。
	SubTaskMaxChars int
	// PreApproved 为 true 表示上层权限策略已就本次调用获得用户批准，
	// 工具内部的二次确认应跳过，避免同一个操作连弹两个框。
	PreApproved bool
	// Agents 为工作区自定义子代理定义，task 工具用它解析 agent 参数。
	Agents []core.AgentDef
}

// Result 工具执行结果。
type Result struct {
	Output string
	// Display 为 true 时以卡片形式重点展示（如 todo 列表、文件预览）。
	Display bool
	Title   string
}

// Tool 一个工具定义。
type Tool struct {
	Name   string
	Desc   string
	Schema string
	Run    func(ctx *Context, args map[string]interface{}) (Result, error)
}

var (
	registry   []Tool
	registryMu sync.RWMutex
)

func register(t Tool) {
	registryMu.Lock()
	registry = append(registry, t)
	registryMu.Unlock()
}

// RegisterDynamic 运行时注册工具（MCP 等扩展使用）；同名工具会被替换。
// RegisterDynamic 注册运行时工具（MCP 等）。同名直接替换并写日志——
// 覆盖内建工具危害大（如顶掉 run_command 就相当于把安全确认体系架空），
// 以前静默替换，现在至少让用户在日志里看到。
func RegisterDynamic(t Tool) {
	registryMu.Lock()
	for i := range registry {
		if registry[i].Name == t.Name {
			registry[i] = t
			registryMu.Unlock()
			core.Logf("tools: MCP/运行时工具 %q 覆盖了同名内建工具（内建实现已被替换，如需可信内置实现请重启后通过 /mcp 检查）", t.Name)
			return
		}
	}
	registry = append(registry, t)
	registryMu.Unlock()
}

// Unregister 移除运行时注册的工具，返回是否移除了内容。
func Unregister(name string) bool {
	registryMu.Lock()
	defer registryMu.Unlock()
	for i := range registry {
		if registry[i].Name == name {
			registry = append(registry[:i], registry[i+1:]...)
			return true
		}
	}
	return false
}

// All 返回全部工具（副本，避免调用方持有内部切片）。
func All() []Tool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Tool, len(registry))
	copy(out, registry)
	return out
}

// Find 按名称查找工具。
func Find(name string) (Tool, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	for _, t := range registry {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// Defs 返回适用于 OpenAI function calling 的工具定义。
func Defs() []llm.ToolDef {
	all := All()
	out := make([]llm.ToolDef, 0, len(all))
	for _, t := range all {
		out = append(out, llm.ToolDef{
			Type: "function",
			Function: llm.ToolFuncDef{
				Name:        t.Name,
				Description: t.Desc,
				Parameters:  json.RawMessage(t.Schema),
			},
		})
	}
	return out
}

// Get 读取字符串参数。
func Get(args map[string]interface{}, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetInt 读取整型参数。
func GetInt(args map[string]interface{}, key string, def int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case string:
			var f float64
			if json.Unmarshal([]byte(n), &f) == nil {
				return int(f)
			}
		}
	}
	return def
}

// GetBool 读取布尔参数。
func GetBool(args map[string]interface{}, key string, def bool) bool {
	if v, ok := args[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// readOnlyTools 计划模式下允许执行的白名单：只读与待办记录，不触碰磁盘状态。
var readOnlyTools = map[string]bool{
	"read_file":     true,
	"list_dir":      true,
	"glob":          true,
	"grep":          true,
	"repo_map":      true,
	"diagnostics":   true,
	"toolchain":     true,
	"lsp":           true,
	"code_search":   true,
	"calls":         true,
	"skill_list":    true,
	"skill_use":     true,
	"todo_write":    true,
	"memory_list":   true,
	"memory_search": true,
	// 子 Agent 会继承计划模式，因此允许在只读调研时派发任务
	"task": true,
}

// IsReadOnly 判断工具是否为只读（不会改变工作区状态）。
func IsReadOnly(name string) bool { return readOnlyTools[name] }
