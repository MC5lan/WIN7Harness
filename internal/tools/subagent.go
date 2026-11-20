package tools

import (
	"fmt"
	"strings"
	"sync"

	"win7harness/internal/core"
)

// 一次批量派发允许的子任务上限：默认 3，可在设置里调到 8。
// 并行窗口过大会放大 token 消耗与失败面，所以设了硬上限。
const (
	defaultMaxSubTasks = 3
	hardMaxSubTasks    = 8
)

// subTaskLimit 归一化外部传入的上限，越界回落到默认或硬上限。
func subTaskLimit(n int) int {
	if n <= 0 {
		return defaultMaxSubTasks
	}
	if n > hardMaxSubTasks {
		return hardMaxSubTasks
	}
	return n
}

func init() {
	register(Tool{
		Name: "task",
		Desc: "派发子任务给独立的子 Agent 执行。子 Agent 有自己的上下文窗口，适合大范围搜索、独立调研、批量分析等会把主对话撑爆的工作；它只把结论返回给你。单个任务用 prompt；多个互不依赖的任务用 tasks 数组一次派发（默认最多 3 个，可在设置里调到 8），全部 read_only=true 时会并行执行，只有全部只读才并行，涉及写入的任务会退化为串行以免互相踩文件。read_only=true 时子 Agent 只能读不能改（推荐用于调查类任务）。",
		Schema: `{
  "type":"object",
  "properties":{
    "description":{"type":"string","description":"单个任务时三到五个字的任务简短描述"},
    "prompt":{"type":"string","description":"单个任务的完整、自包含说明：目标、范围、要注意什么、期望返回什么格式"},
    "read_only":{"type":"boolean","description":"单个任务是否只读执行，默认 true"},
    "agent":{"type":"string","description":"使用工作区 .harness/agents 里定义的自定义子代理（如 reviewer）。留空则用内置通用子代理"},
    "tasks":{
      "type":"array",
      "description":"批量派发（默认最多 3 个，上限见设置）。与 prompt 二选一，互不依赖的任务才放进来",
      "items":{
        "type":"object",
        "properties":{
          "description":{"type":"string","description":"三到五个字的任务简短描述"},
          "prompt":{"type":"string","description":"完整、自包含的任务说明"},
          "read_only":{"type":"boolean","description":"是否只读，默认 true"},
          "agent":{"type":"string","description":"可选：自定义子代理名"}
        },
        "required":["prompt"]
      }
    }
  }
}`,
		Run: runTask,
	})
}

type subTask struct {
	desc     string
	prompt   string
	readOnly bool
	agent    *core.AgentDef
}

// BatchTask 交给上层并行执行的写子任务（隔离副本 + 合并回写）。
type BatchTask struct {
	Desc   string
	Prompt string
}

func parseSubTasks(ctx *Context, args map[string]interface{}, limit int) ([]subTask, error) {
	limit = subTaskLimit(limit)
	var list []subTask
	if raw, ok := args["tasks"].([]interface{}); ok && len(raw) > 0 {
		var skipped []string
		for _, item := range raw {
			m, ok := item.(map[string]interface{})
			if !ok {
				skipped = append(skipped, fmt.Sprintf("第 %d 项不是对象，已跳过", len(skipped)+len(list)+1))
				continue
			}
			prompt := strings.TrimSpace(Get(m, "prompt"))
			if prompt == "" {
				skipped = append(skipped, fmt.Sprintf("第 %d 项缺少 prompt，已跳过", len(list)+len(skipped)+1))
				continue
			}
			desc := strings.TrimSpace(Get(m, "description"))
			if desc == "" {
				desc = fmt.Sprintf("子任务%d", len(list)+1)
			}
			ro := true
			if v, ok := m["read_only"].(bool); ok {
				ro = v
			} else if s, ok := m["read_only"].(string); ok {
				ro = parseBoolish(s, true)
			}
			def, derr := resolveAgent(ctx, Get(m, "agent"))
			if derr != nil {
				return nil, derr
			}
			list = append(list, subTask{desc: desc, prompt: prompt, readOnly: ro, agent: def})
		}
		if len(skipped) > 0 {
			// 不静默丢弃：模型必须知道哪些任务没接到活
			return nil, fmt.Errorf("%d 个子任务无效：%s", len(skipped), strings.Join(skipped, "；"))
		}
	}
	if len(list) == 0 {
		prompt := strings.TrimSpace(Get(args, "prompt"))
		if prompt == "" {
			return nil, fmt.Errorf("prompt 或 tasks 至少要有一个")
		}
		desc := strings.TrimSpace(Get(args, "description"))
		if desc == "" {
			desc = "子任务"
		}
		ro := true
		if v, ok := args["read_only"].(bool); ok {
			ro = v
		} else if s, ok := args["read_only"].(string); ok {
			ro = parseBoolish(s, true)
		}
		def, derr := resolveAgent(ctx, Get(args, "agent"))
		if derr != nil {
			return nil, derr
		}
		list = append(list, subTask{desc: desc, prompt: prompt, readOnly: ro, agent: def})
	}
	if len(list) > limit {
		return nil, fmt.Errorf("一次最多派发 %d 个子任务（可在设置里调整，上限 %d），收到 %d 个", limit, hardMaxSubTasks, len(list))
	}
	return list, nil
}

// parseBoolish 解析模型给的 "true"/"false"/"1"/"0"/"yes"/"no"/"on"/"off" 字符串。
// 解析不出来就退回 defVal（默认 true，最安全的默认）。
func parseBoolish(s string, defVal bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on", "y":
		return true
	case "false", "0", "no", "off", "n":
		return false
	}
	return defVal
}

// resolveAgent 解析自定义子代理名；未指定时返回 nil，未知名称直接报错并列出可用项，
// 避免模型拼错名字后悄悄退回通用子代理、结果看起来"没按定义执行"。
func resolveAgent(ctx *Context, name string) (*core.AgentDef, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	def, ok := core.FindAgent(ctx.Agents, name)
	if !ok {
		var names []string
		for _, d := range ctx.Agents {
			names = append(names, d.Name)
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("未找到自定义子代理 %q：工作区 .harness/agents 下没有定义", name)
		}
		return nil, fmt.Errorf("未找到自定义子代理 %q，可用的有：%s", name, strings.Join(names, ", "))
	}
	return &def, nil
}

func runTask(ctx *Context, args map[string]interface{}) (Result, error) {
	list, err := parseSubTasks(ctx, args, ctx.MaxSubTasks)
	if err != nil {
		return Result{}, err
	}
	if ctx.Spawn == nil {
		return Result{}, fmt.Errorf("当前上下文不支持派发子任务（子 Agent 内不能再派生子 Agent）")
	}

	// 只有全部只读时才并行：写类子任务并行会争抢同一批文件与确认弹窗。
	parallel := len(list) > 1
	hasWrite := false
	for _, t := range list {
		if !t.readOnly {
			hasWrite = true
			parallel = false
			break
		}
	}
	// 有写操作时不再无脑串行：上层支持隔离副本执行就并行跑、最后合并，
	// 免得三个改文件的子任务排队等，用户干等。
	useBatch := hasWrite && len(list) > 1 && ctx.SpawnBatch != nil
	for _, t := range list {
		if t.agent != nil {
			// 自定义子代理的人设与工具限制是它存在的理由，不能走隔离副本合并路径。
			useBatch = false
		}
	}
	if useBatch {
		batch := make([]BatchTask, len(list))
		for i, t := range list {
			batch[i] = BatchTask{Desc: t.desc, Prompt: t.prompt}
		}
		if ctx.Progress != nil {
			ctx.Progress(fmt.Sprintf("并行派发 %d 个写子任务（隔离副本，结束合并）", len(list)))
		}
		out, err := ctx.SpawnBatch(batch)
		if err != nil {
			return Result{}, err
		}
		return Result{Output: out}, nil
	}
	if ctx.Progress != nil {
		if parallel {
			ctx.Progress(fmt.Sprintf("并行派发 %d 个子任务", len(list)))
		} else {
			ctx.Progress("派发子任务：" + list[0].desc)
		}
	}

	type outcome struct {
		text string
		err  error
	}
	res := make([]outcome, len(list))

	if parallel {
		// 并发度跟随上限，避免用户把上限调小后仍然一次性全放出去。
		sem := make(chan struct{}, subTaskLimit(ctx.MaxSubTasks))
		var wg sync.WaitGroup
		for i := range list {
			wg.Add(1)
			sem <- struct{}{}
			i := i // 每次迭代独立：外层闭包按引用捕获，必须局部化
			core.SafeGo("tools/subagent.go:214", func() {
				func(i int) {
					defer wg.Done()
					defer func() { <-sem }()
					t := list[i]
					out, err := spawnOne(ctx, t)
					res[i] = outcome{text: out, err: err}
				}(i)
			})
		}
		wg.Wait()
	} else {
		for i, t := range list {
			out, err := spawnOne(ctx, t)
			res[i] = outcome{text: out, err: err}
		}
	}

	var b strings.Builder
	fails := 0
	for i, r := range res {
		if r.err != nil {
			fails++
			fmt.Fprintf(&b, "【子任务 %s 失败】\n错误：%s\n\n", list[i].desc, r.err.Error())
			continue
		}
		out := strings.TrimSpace(r.text)
		if out == "" {
			out = "（完成，但没有返回文本）"
		}
		if len(list) == 1 {
			fmt.Fprintf(&b, "【子任务 %s 的结论】\n%s", list[i].desc, out)
		} else {
			fmt.Fprintf(&b, "【子任务 %d/%d：%s】\n%s\n\n", i+1, len(list), list[i].desc, out)
		}
	}
	if fails == len(list) {
		return Result{}, fmt.Errorf("全部子任务失败：%s", strings.TrimSpace(res[0].err.Error()))
	}
	return Result{Output: strings.TrimSpace(b.String())}, nil
}

// spawnOne 按是否指定自定义子代理选择派生入口。
func spawnOne(ctx *Context, t subTask) (string, error) {
	if t.agent != nil && ctx.SpawnNamed != nil {
		return ctx.SpawnNamed(t.agent, t.prompt, t.desc, t.readOnly)
	}
	return ctx.Spawn(t.prompt, t.desc, t.readOnly)
}
