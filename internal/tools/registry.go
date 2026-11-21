// Package tools 实现 Harness 的本地工具集（读写文件、检索、执行命令等）。
// 工具定义同时用于 LLM function calling 与界面展示。
package tools

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
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
	// SkillLoaded / MarkSkillLoaded 让 skill_use 具备会话级去重：
	// 同一技能第二次调用只回一句“已加载”，不再把正文重复塞进上下文。
	SkillLoaded     func(name string) bool
	MarkSkillLoaded func(name string)
	// LoadTool 由引擎注入：core profile 下按需把扩展工具加入下一轮工具定义。
	// 返回 false 表示当前模式/白名单不允许加载。
	LoadTool func(name string) bool
}

// Result 工具执行结果。
type Result struct {
	Output string
	// Display 为 true 时以卡片形式重点展示（如 todo 列表、文件预览）。
	Display bool
	Title   string
	// Trust 标记结果来源：system / user / project / model-notes / external。
	// 空值由工具元数据补齐；外部内容统一作为不可信资料注入。
	Trust string
	// Detail 是只给界面看的结构化细节（例如 edit_file/apply_patch 的差异），
	// 不进入模型上下文，避免为了展示 diff 再重复喂几 KB。
	Detail string
}

// Tool 一个工具定义。
type Tool struct {
	Name   string
	Desc   string
	Schema string
	Run    func(ctx *Context, args map[string]interface{}) (Result, error)

	// 以下字段由 defaultToolMeta 在注册时补齐；动态工具（MCP）单独赋值。
	Category     string // filesystem / exec / analysis / network / memory / skills / agent ...
	ReadOnly     bool   // 计划模式是否允许；不代表绝对无副作用
	ParallelSafe bool   // 是否可与其他只读工具并行
	Trust        string // system / external ...
	MaxOutput    int    // 0 = 用全局预算
	Hidden       bool   // 不进入常规工具清单（保留给内部工具）
	Core         bool   // core profile 是否保留
}

var (
	registry   []Tool
	registryMu sync.RWMutex
	// registryRev 在工具新增/替换/卸载时递增，供上层缓存工具定义。
	registryRev uint64
)

// 工具元数据：集中一处维护只读/并行/分类，避免 agent/batch.go 与 registry.go
// 各有一份白名单，改一处漏一处。
var defaultToolMeta = map[string]Tool{
	"read_file":      {Category: "filesystem", ReadOnly: true, ParallelSafe: true, Trust: "project", Core: true},
	"write_file":     {Category: "filesystem", Trust: "system", Core: true},
	"edit_file":      {Category: "filesystem", Trust: "system", Core: true},
	"delete_path":    {Category: "filesystem", Trust: "system", Core: true},
	"apply_patch":    {Category: "filesystem", Trust: "system", Core: true},
	"list_dir":       {Category: "filesystem", ReadOnly: true, ParallelSafe: true, Trust: "project", Core: true},
	"glob":           {Category: "filesystem", ReadOnly: true, ParallelSafe: true, Trust: "project", Core: true},
	"grep":           {Category: "filesystem", ReadOnly: true, ParallelSafe: true, Trust: "project", Core: true},
	"todo_write":     {Category: "plan", ReadOnly: true, Trust: "system", Core: true},
	"run_command":    {Category: "exec", Trust: "system", Core: true},
	"run_tests":      {Category: "exec", Trust: "system", Core: true},
	"diagnostics":    {Category: "analysis", ReadOnly: true, ParallelSafe: true, Trust: "project"},
	"lsp":            {Category: "analysis", ReadOnly: true, ParallelSafe: true, Trust: "project"},
	"code_search":    {Category: "analysis", ReadOnly: true, ParallelSafe: true, Trust: "project", Core: true},
	"calls":          {Category: "analysis", ReadOnly: true, ParallelSafe: true, Trust: "project"},
	"repo_map":       {Category: "analysis", ReadOnly: true, ParallelSafe: true, Trust: "project", Core: true},
	"toolchain":      {Category: "analysis", ReadOnly: true, ParallelSafe: true, Trust: "system"},
	"datatable":      {Category: "analysis", ReadOnly: true, ParallelSafe: true, Trust: "project"},
	"quality":        {Category: "analysis", ReadOnly: true, ParallelSafe: true, Trust: "project"},
	"read_image":     {Category: "multimodal", ReadOnly: true, ParallelSafe: true, Trust: "project"},
	"read_artifact":  {Category: "filesystem", ReadOnly: true, ParallelSafe: true, Trust: "external", Core: true},
	"web_fetch":      {Category: "network", ReadOnly: true, ParallelSafe: true, Trust: "external"},
	"web_search":     {Category: "network", ReadOnly: true, ParallelSafe: true, Trust: "external"},
	"history_search": {Category: "history", ReadOnly: true, ParallelSafe: true, Trust: "external"},
	"memory_list":    {Category: "memory", ReadOnly: true, ParallelSafe: true, Trust: "model-notes"},
	"memory_search":  {Category: "memory", ReadOnly: true, ParallelSafe: true, Trust: "model-notes"},
	"memory_save":    {Category: "memory", Trust: "model-notes"},
	"skill_list":     {Category: "skills", ReadOnly: true, ParallelSafe: true, Trust: "user", Core: true},
	"skill_use":      {Category: "skills", ReadOnly: true, ParallelSafe: true, Trust: "user", Core: true},
	"tool_search":    {Category: "skills", ReadOnly: true, ParallelSafe: true, Trust: "system", Core: true},
	"tool_load":      {Category: "skills", ReadOnly: true, Trust: "system", Core: true},
	"task":           {Category: "agent", ReadOnly: true, Trust: "system", Core: true},
	"task_list":      {Category: "agent", ReadOnly: true, ParallelSafe: true, Trust: "system"},
	"task_output":    {Category: "agent", ReadOnly: true, ParallelSafe: true, Trust: "system"},
	"task_kill":      {Category: "agent", Trust: "system"},
}

// planSafeTools 是计划模式真正允许的工具集合。它比 ReadOnly 更窄：
// 网页/搜索/历史检索虽然不改本地状态，但会联网或扩大输入面，计划模式先不放。
// 这里刻意沿用旧版计划模式白名单，避免“升级后计划模式突然能联网”的隐性行为变化。
var planSafeTools = map[string]bool{
	"read_file":     true,
	"read_artifact": true,
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
	"tool_search":   true,
	"tool_load":     true,
	"todo_write":    true,
	"memory_list":   true,
	"memory_search": true,
	"task":          true,
}

var builtinNames = func() map[string]bool {
	m := make(map[string]bool, len(defaultToolMeta))
	for n := range defaultToolMeta {
		m[n] = true
	}
	return m
}()

func register(t Tool) {
	if m, ok := defaultToolMeta[t.Name]; ok {
		mergeMeta(&t, m)
	}
	registryMu.Lock()
	registry = append(registry, t)
	registryRev++
	registryMu.Unlock()
}

func mergeMeta(t *Tool, m Tool) {
	if t.Category == "" {
		t.Category = m.Category
	}
	if !t.ReadOnly {
		t.ReadOnly = m.ReadOnly
	}
	if !t.ParallelSafe {
		t.ParallelSafe = m.ParallelSafe
	}
	if t.Trust == "" {
		t.Trust = m.Trust
	}
	if t.MaxOutput == 0 {
		t.MaxOutput = m.MaxOutput
	}
	if !t.Hidden {
		t.Hidden = m.Hidden
	}
	if !t.Core {
		t.Core = m.Core
	}
}

// RegisterDynamic 注册运行时工具（MCP 等）。内建工具不可被覆盖：
// 顶掉 run_command 等于把安全确认体系架空，必须拒绝而不是只写日志。
func RegisterDynamic(t Tool) bool {
	if builtinNames[t.Name] {
		core.Logf("tools: 拒绝运行时工具 %q 覆盖内建工具", t.Name)
		return false
	}
	if t.Trust == "" {
		t.Trust = "external"
	}
	if t.Category == "" {
		t.Category = "mcp"
	}
	registryMu.Lock()
	for i := range registry {
		if registry[i].Name == t.Name {
			registry[i] = t
			registryRev++
			registryMu.Unlock()
			core.Logf("tools: 运行时工具 %q 已替换同名动态工具", t.Name)
			return true
		}
	}
	registry = append(registry, t)
	registryRev++
	registryMu.Unlock()
	return true
}

// Unregister 移除运行时注册的工具，返回是否移除了内容。
func Unregister(name string) bool {
	if builtinNames[name] {
		return false
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	for i := range registry {
		if registry[i].Name == name {
			registry = append(registry[:i], registry[i+1:]...)
			registryRev++
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

// Count 返回当前工具数量（含动态 MCP）。
func Count() int {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return len(registry)
}

// Revision 返回工具注册表版本，供上层缓存工具定义。
func Revision() uint64 {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registryRev
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

// 工具 profile。
const (
	ProfileFull   = "full"
	ProfileCore   = "core"
	ProfilePlan   = "plan"
	ProfileSub    = "subagent"
	ProfileCustom = "custom"
)

// Defs 返回全部工具定义（兼容旧调用；新代码用 DefsFor 裁剪）。
func Defs() []llm.ToolDef {
	return DefsFor(ProfileFull, nil)
}

// DefsFor 按 profile 与白名单返回工具定义，并按名称稳定排序。
// allow 为 nil 表示不按名称过滤；空 map 表示一个都不允许（不要传空 map 表示“不过滤”）。
func DefsFor(profile string, allow map[string]bool) []llm.ToolDef {
	all := All()
	sort.SliceStable(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	out := make([]llm.ToolDef, 0, len(all))
	for _, t := range all {
		if t.Hidden || !profileAllows(profile, t) {
			continue
		}
		if allow != nil && !allow[t.Name] {
			continue
		}
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

func profileAllows(profile string, t Tool) bool {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case ProfilePlan:
		return planSafeTools[t.Name]
	case ProfileCore:
		return t.Core
	case ProfileSub:
		return t.ReadOnly
	default:
		return true
	}
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

// IsReadOnly 判断工具是否为只读（不会改变工作区状态）。
func IsReadOnly(name string) bool {
	if t, ok := Find(name); ok {
		return t.ReadOnly
	}
	return false
}

// IsPlanSafe 判断计划模式是否允许该工具（比只读更窄，不含联网/外部检索）。
func IsPlanSafe(name string) bool { return planSafeTools[name] }

// IsParallelSafe 判断工具能否与其它只读工具并行。
func IsParallelSafe(name string) bool {
	if t, ok := Find(name); ok {
		return t.ParallelSafe
	}
	return false
}

// IsBuiltin 判断是否为内建工具名。
func IsBuiltin(name string) bool { return builtinNames[name] }

// TrustOf 返回工具结果的默认信任级别。
func TrustOf(name string) string {
	if t, ok := Find(name); ok && t.Trust != "" {
		return t.Trust
	}
	return "external"
}

// CoreTools 返回 core profile 的工具名，供文档/自检使用。
func CoreTools() []string {
	var out []string
	for n, m := range defaultToolMeta {
		if m.Core {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}
