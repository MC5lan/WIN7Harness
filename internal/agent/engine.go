// Package agent 实现 Harness 的对话-工具循环（Agent Loop）。
// 与界面解耦：通过事件回调把流式文本、工具调用、错误上报给 UI。
package agent

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"win7harness/internal/codeidx"
	"win7harness/internal/core"
	"win7harness/internal/llm"
	"win7harness/internal/skills"
	"win7harness/internal/tools"
)

// Event 是 Agent 循环向上层上报的事件。
type Event struct {
	Kind string // reasoning | content | tool_start | tool_end | turn_end | error | plan | status
	Text string
	ID   string
	Name string
	Args string
	Err  bool
	// Dur 仅 tool_end 事件携带，本次工具实际耗时。
	Dur time.Duration
	// Usage 仅 turn_end 事件携带，供界面做成本统计。
	Usage llm.Usage
}

// Engine 驱动一次对话。
type Engine struct {
	Client  *llm.Client
	Model   string
	WorkDir string
	Temp    float64
	MaxTok  int
	// PriceIn / PriceOut 是用户配置的自定义单价（美元/百万 token）。
	// 两者都为 0 时按 DefaultPrice 估算；不再内置任何模型价目表。
	PriceIn  float64
	PriceOut float64
	// PlanMode 计划模式：只允许只读工具，任何写盘/执行类操作一律拒绝。
	PlanMode bool
	// MaxSubTasks 批量派发子任务的上限，0 表示用默认值（3）。
	MaxSubTasks int
	// VerifyMode 自动验证开关：off / auto，空值时读环境变量 W7H_VERIFY。
	VerifyMode string
	// MaxVerifyRounds 自动验证最多重试轮数，0 表示用默认值（3）。
	MaxVerifyRounds int
	// VerifyTimeout 单步验证（构建或测试）超时，0 表示 5 分钟。
	VerifyTimeout time.Duration
	// FastModel 轻任务模型（压缩摘要、只读子代理等）。为空表示不启用路由。
	FastModel string
	// RouterOn 是否启用模型路由：打开后轻任务走 FastModel，主对话仍用 Model。
	RouterOn bool
	// SubTaskMaxChars 单个子任务结论的字数上限，0 表示用默认值（6000）。
	SubTaskMaxChars int
	// Policy 权限与审批策略；nil 表示不额外限制（保持旧行为）。
	Policy *core.Policy
	// toolAllow 自定义子代理的工具白名单；nil 表示不限制。
	toolAllow map[string]bool
	// SystemExtra 追加到系统提示末尾的额外职责说明（自定义子代理用）。
	SystemExtra string

	// pendingImgs 暂存工具读到的图片 data URL，本轮工具批次结束后统一回灌。
	imgMu       sync.Mutex
	pendingImgs []string

	// board 是给任务侧栏用的实时任务视图（懒初始化）。
	board     *TaskBoard
	boardOnce sync.Once

	// 本轮是否动过写类操作（见 verify.go）；lastHitRate 是最近一次缓存命中率。
	verifyMu      sync.Mutex
	wroteThisTurn bool
	// changedPaths 是本轮写类工具涉及的文件（相对/绝对均可），
	// 自动验证据此只跑受影响包；writeUntracked 表示有无法定位的写操作
	// （run_command、gofmt 等），此时退回全量验证。
	changedPaths   map[string]bool
	writeUntracked bool
	lastHitRate    float64
	curRound       int
	// compactCount 本次会话累计自动压缩次数。界面要能看见"刚刚因为太长压缩过"，
	// 否则用户只会觉得模型突然变笨了。
	compactCount int

	// 累计 token 用量（跨轮），用于成本估算。
	usageMu sync.Mutex
	tokIn   int
	tokOut  int
	turns   int
	// 提示缓存命中量（跨轮），用于成本看板算"缓存省了多少"。
	tokCached int
	tokCacheW int
	// 快速模型（压缩/只读子代理）的用量单独记账，便于按各自单价算钱
	fastTokIn  int
	fastTokOut int
	fastTurns  int
	// 子代理用量单独累计，用于回答"这次派发花了多少"
	subTokIn  int
	subTokOut int
	subTurns  int
	// 子代理里命中的快速模型 token（用于按 fast/main 分价计费）
	subFastTokIn  int
	subFastTokOut int

	// 子 Agent 相关：depth=0 为主 Agent，>0 表示由 task 工具派生。
	depth    int
	maxRound int
	// MaxToolRounds 主 Agent 单次请求的工具轮数上限；0 表示不限制（默认）。
	MaxToolRounds int
	// ContextWindow 模型上下文窗口（token），用于算自动压缩阈值；0 用 128k 兜底。
	ContextWindow int
	// SubAgentTimeoutSec 单个子代理任务的超时（秒）；0 = 默认 20 分钟，上限 2 小时。
	SubAgentTimeoutSec int
	runCtx             context.Context

	// Confirm 在危险操作前询问用户（阻塞直到用户响应）。
	Confirm func(title, detail string) bool
	// ConfirmDiff 是带行号与 hunk 边界的差分确认，返回被接受的 hunk 下标。
	// nil 表示拒绝；空切片表示全不选；allowPartial 为 true 时界面提供逐段开关。
	ConfirmDiff func(title, path string, hunks []core.DiffHunk, allowPartial bool) []int
	// PlanApprove 是计划模式的审批闸门。引擎发出 plan 事件后阻塞等待，
	// 返回 approve / reject / modify；modify 时 modified 为用户改后的方案。
	// nil 时保持旧行为（不阻塞，用户靠输入"按上面的计划开始执行"继续）。
	PlanApprove func(title, plan string, steps int) (decision, modified string)
	// Emit 事件回调，必须快速返回（内部已做异步缓冲）。
	Emit func(Event)

	mu       sync.Mutex
	Messages []llm.Message
	cancel   context.CancelFunc
	running  bool

	// fail 记录连续失败，用于拦住"同一调用反复重试"。
	fail failureTracker

	// 耗时与 token 归因（见 stats.go）
	toolMu         sync.Mutex
	toolStats      map[string]*ToolStat
	toolTotalMs    int64
	modelTotalMs   int64
	modelTokens    int
	slowestModelMs int64
}

// New 创建引擎。
func New(c *llm.Client, model, workDir string) *Engine {
	return &Engine{
		Client:  c,
		Model:   model,
		WorkDir: workDir,
		Temp:    0.3,
		MaxTok:  4096,
	}
}

// Running 是否正在生成。
func (e *Engine) Running() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// RefreshProxy 在系统代理变化/睡眠恢复后更新客户端连接池的代理策略。
// 已建立的连接不会自动换代理，下一次请求会走新设置。
func (e *Engine) RefreshProxy(mode, rawURL string) {
	if e == nil || e.Client == nil {
		return
	}
	e.Client.SetProxy(mode, rawURL)
}

// Stop 中断当前生成。
func (e *Engine) Stop() {
	e.mu.Lock()
	c := e.cancel
	e.mu.Unlock()
	if c != nil {
		c()
	}
}

// Reset 清空会话（切换/回灌历史前调用）。
// 全量清空语义：消息 + 失败计数 + 写标志 + 压缩计数 + 全部统计与用量账本，
// 否则“新会话”看板接着累加、上一会话的同参数失败硬护栏跨会话误伤。
func (e *Engine) Reset() {
	e.mu.Lock()
	e.Messages = nil
	e.mu.Unlock()
	e.fail.reset()
	e.verifyMu.Lock()
	e.wroteThisTurn = false
	e.lastHitRate = 0
	e.curRound = 0
	e.compactCount = 0
	e.verifyMu.Unlock()
	e.usageMu.Lock()
	e.tokIn, e.tokOut, e.turns = 0, 0, 0
	e.tokCached, e.tokCacheW = 0, 0
	e.fastTokIn, e.fastTokOut, e.fastTurns = 0, 0, 0
	e.subTokIn, e.subTokOut, e.subTurns = 0, 0, 0
	e.subFastTokIn, e.subFastTokOut = 0, 0
	e.usageMu.Unlock()
	e.toolMu.Lock()
	e.toolStats = nil
	e.toolTotalMs, e.modelTotalMs, e.modelTokens, e.slowestModelMs = 0, 0, 0, 0
	e.toolMu.Unlock()
}

// Retry 丢弃最后一条用户消息之后的所有上下文，按同样的输入重新生成一次。
// 返回 false 表示当前正在生成、或上下文里没有用户消息可重试。
func (e *Engine) Retry() bool {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return false
	}
	idx := -1
	for i := len(e.Messages) - 1; i >= 0; i-- {
		if e.Messages[i].Role == "user" {
			idx = i
			break
		}
	}
	if idx < 0 {
		e.mu.Unlock()
		return false
	}
	e.Messages = e.Messages[:idx+1]
	e.resetWriteFlag()
	ctx, cancel := context.WithCancel(context.Background())
	e.running = true
	e.cancel = cancel
	e.mu.Unlock()
	e.runAsync(ctx)
	return true
}

// History 返回消息快照。
func (e *Engine) History() []llm.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]llm.Message, len(e.Messages))
	copy(out, e.Messages)
	return out
}

// Send 追加用户消息并启动 Agent 循环（异步）。
func (e *Engine) Send(userText string) {
	e.SendWithImages(userText, nil)
}

func (e *Engine) emit(ev Event) {
	if e.Emit != nil {
		e.Emit(ev)
	}
}

// roundLimit 返回本引擎的单次请求工具轮数上限；0 表示不限制。
//
// 子 Agent 恒用 maxRound（必须封顶，否则一个跑飞的子代理能烧掉主预算）；
// 主 Agent 用 MaxToolRounds，默认 0 = 不限。
//
// 过去这里是硬编码 40 且到点直接报错停止。做稍大一点的重构必然撞顶：
// 一次"读文件 → 改 → 跑构建 → 修"就是好几轮，40 轮只够动十几个文件。
// 真正的护栏交给预算（MaxCost / BudgetTokens）和"同一错误反复出现"的拦截，
// 不该用轮数去猜任务大小。
func (e *Engine) roundLimit() int {
	if e.maxRound > 0 {
		return e.maxRound
	}
	return e.MaxToolRounds
}

func (e *Engine) loop(ctx context.Context) {
	e.mu.Lock()
	e.runCtx = ctx
	e.mu.Unlock()
	// 自动验证的重试次数按"这一轮用户请求"计数，不能跨请求累积，
	// 否则用户第二次发消息时就永远轮不到验证了。
	verifyTries := 0
	limit := e.roundLimit()
	for round := 0; ; round++ {
		// 只对"显式设了上限"的引擎生效；limit<=0 是完全不检查。
		// 撞顶不再直接判失败：先问用户要不要继续（同意就把额度翻倍），
		// 用户拒绝或没有确认通道时才停——由用户决定，而不是由常量决定。
		if limit > 0 && round >= limit {
			note := fmt.Sprintf("已连续执行 %d 轮工具调用，达到你设置的上限。", round)
			if e.Confirm != nil && e.Confirm("继续执行？", note+"\n\n继续会消耗更多 token；也可以先停下看结果。") {
				limit += limit
				e.emit(Event{Kind: "notice", Text: fmt.Sprintf("%s已延长到 %d 轮。", note, limit)})
			} else {
				e.emit(Event{Kind: "error", Err: true,
					Text: fmt.Sprintf("已暂停：本次请求执行了 %d 轮工具调用，达到上限（%d 轮）。"+
						"结果都还在，发一句「继续」就能接着做；也可以在设置里把上限调大或设为 0（不限制）。", round, e.roundLimit())})
				return
			}
		}
		e.verifyMu.Lock()
		e.curRound = round + 1
		e.verifyMu.Unlock()
		if ctx.Err() != nil {
			e.emit(Event{Kind: "status", Text: "已停止"})
			return
		}
		// 预算护栏：每轮调用模型前检查，超限即停，不再产生费用。
		if stop, note := e.CheckBudget(); stop {
			e.emit(Event{Kind: "budget", Text: note})
			return
		} else if note != "" {
			e.emit(Event{Kind: "notice", Text: note})
		}
		// 自动压缩：接近上下文水线就先压缩早期对话，别撞网关的长度上限。
		// 此前没有任何调用点——Memory 笔记里宣称的"已压缩 M 次"永远是 0，
		// 长会话一路涨到 400 才停。
		if e.compactIfNeeded(ctx) {
			e.emit(Event{Kind: "notice", Text: "上下文已自动压缩（早期对话转摘要），token 用量已重置。"})
		}

		msgs := e.buildContext()
		var content, reasoning strings.Builder
		var calls []pendingCall

		e.emit(Event{Kind: "status", Text: "思考中…"})

		chatStart := time.Now()
		usage, err := e.Client.Chat(ctx, msgs, tools.Defs(), e.Temp, e.MaxTok, llm.Handlers{
			OnReasoning: func(s string) {
				reasoning.WriteString(s)
				e.emit(Event{Kind: "reasoning", Text: s})
			},
			OnContent: func(s string) {
				content.WriteString(s)
				e.emit(Event{Kind: "content", Text: s})
			},
			OnToolCall: func(name, args, id string) {
				calls = append(calls, pendingCall{id: id, name: name, args: args})
			},
		})

		// 归因统计：这一段是"模型在想"，跟工具耗时分开记
		e.recordModel(time.Since(chatStart), usage.CompletionTokens)
		if err != nil {
			if ctx.Err() != nil {
				e.emit(Event{Kind: "status", Text: "已停止"})
				return
			}
			msg := friendlyErr(err)
			if e.hasImagesInContext() {
				msg += visionHint(err)
			}
			e.emit(Event{Kind: "error", Text: msg, Err: true})
			// 把已生成内容保留下来，避免用户丢失
			if content.Len() > 0 || reasoning.Len() > 0 {
				e.mu.Lock()
				e.Messages = append(e.Messages, llm.Message{
					Role: "assistant", Content: content.String(), Reasoning: reasoning.String(),
				})
				e.mu.Unlock()
			}
			return
		}

		assistant := llm.Message{
			Role:      "assistant",
			Content:   content.String(),
			Reasoning: reasoning.String(),
		}
		for _, c := range calls {
			assistant.ToolCalls = append(assistant.ToolCalls, llm.ToolCall{
				ID:   c.id,
				Type: "function",
				Function: llm.FunctionCall{
					Name:      c.name,
					Arguments: c.args,
				},
			})
		}
		e.mu.Lock()
		e.Messages = append(e.Messages, assistant)
		e.mu.Unlock()

		e.addUsage(usage)
		if len(calls) == 0 {
			// 模型准备收尾：如果本轮改过代码，先自动跑一次构建 + 测试。
			// 失败就把失败摘要回灌成一条新消息，让它在同一轮里继续修，
			// 而不是把"改完了但编译不过"直接交给用户。
			if report, ran := e.autoVerify(ctx, &verifyTries); ran && report != "" {
				e.mu.Lock()
				e.Messages = append(e.Messages, llm.Message{Role: "user", Content: report})
				e.mu.Unlock()
				continue
			}
			// 计划模式下，模型给出的改动方案单独发一个 plan 事件，并在有审批闸门时
			// 阻塞等待用户批准/拒绝/修改。普通回答不触发，避免打扰。
			if e.PlanMode {
				if txt := strings.TrimSpace(content.String()); looksLikePlan(txt) {
					title, steps := planTitle(txt), planStepCount(txt)
					e.emit(Event{Kind: "plan", Text: txt, Name: title, Args: fmt.Sprintf("%d", steps)})
					if e.PlanApprove != nil {
						decision, modified := e.PlanApprove(title, txt, steps)
						var feedback string
						switch decision {
						case "approve":
							// 退出只读：既清 PlanMode，也要把进入时设成 PermPlan 的策略恢复，
							// 否则批准后写工具仍被 policy.Decide 拒绝（旧实现漏了这一步）。
							e.PlanMode = false
							if e.Policy != nil && e.Policy.Mode == core.PermPlan {
								e.Policy.Mode = core.PermAsk
							}
							feedback = "用户已批准该方案，请立即开始实施。\n\n方案原文：\n" + txt
						case "modify":
							feedback = "用户修改了方案，请按下面的新方案重新给出最终步骤并等待确认：\n" + modified
						default:
							feedback = "用户拒绝了该方案。请先说明拒绝原因或给出另一套方案，不要直接开始写文件。"
						}
						e.mu.Lock()
						e.Messages = append(e.Messages, llm.Message{Role: "user", Content: feedback})
						e.mu.Unlock()
						// 继续循环：下一轮调用会把反馈送进模型，由它决定实施/改方案/停手。
						continue
					}
				}
			}
			e.emit(Event{Kind: "turn_end", Text: fmt.Sprintf("%d", usage.TotalTokens), Usage: usage})
			e.runTurnEndHooks()
			return
		}

		// 工具执行：只读工具并行，写类工具串行，结果按原始顺序写回上下文。
		// 即使本轮已被用户取消也不 early-return：execBatch 会对被取消的工具
		// 回填占位 tool 消息，保住 assistant(tool_calls) 后必有 tool 响应的协议不变量。
		for _, c := range calls {
			if ctx.Err() != nil {
				e.emit(Event{Kind: "status", Text: "已停止"})
			}
			e.emit(Event{Kind: "tool_start", ID: c.id, Name: c.name, Args: c.args})
		}
		outs, durs := e.execBatch(ctx, calls)
		for i, c := range calls {
			out := outs[i]
			e.emit(Event{Kind: "tool_end", ID: c.id, Name: c.name, Text: out,
				Err: strings.HasPrefix(out, "错误："), Dur: durs[i]})
			e.mu.Lock()
			e.Messages = append(e.Messages, llm.Message{
				Role:       "tool",
				ToolCallID: c.id,
				Name:       c.name,
				Content:    out,
			})
			e.mu.Unlock()
		}
		e.flushPendingImages()
	}
}

// execTool 是 execToolRaw 的外层：负责把失败翻译成诊断，并在同一调用
// 反复以同一原因失败时直接拦下。
func (e *Engine) execTool(name, rawArgs string) string {
	key := name + "|" + argFingerprint(rawArgs)
	if msg, block := e.fail.check(key); block {
		return msg
	}
	out := e.execToolRaw(name, rawArgs)
	if strings.HasPrefix(out, "错误：") {
		hint, _ := e.fail.note(name, key, out)
		return Diagnose(name, out).Format(out) + hint
	}
	e.fail.reset()
	return out
}

// writePathsForTool 从工具参数里提取写入路径，供“只验证受影响包”使用。
// 提取不到时返回空，MarkWrite 会把本轮标记为不可定位、退回全量验证。
func writePathsForTool(name string, args map[string]interface{}) []string {
	switch name {
	case "write_file", "edit_file", "delete_path":
		if p := strings.TrimSpace(tools.Get(args, "path")); p != "" {
			return []string{p}
		}
	case "apply_patch":
		return tools.PatchPaths(tools.Get(args, "patch"))
	}
	return nil
}

func (e *Engine) execToolRaw(name, rawArgs string) (out string) {
	start := time.Now()
	// 崩溃排查：GUI 没有控制台，硬退出时日志里至少留下“最后在跑哪个工具”。
	// 参数只记前 240 字，避免把大段文件内容写进日志。
	defer func() {
		core.Logf("[tool] end %s %s err=%v", name, time.Since(start).Round(time.Millisecond), strings.HasPrefix(out, "错误"))
	}()
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprintf("错误：工具 %s 内部异常：%v", name, r)
		}
	}()
	core.Logf("[tool] start %s %s", name, clipLog(rawArgs, 240))
	if e.PlanMode && !tools.IsReadOnly(name) {
		return fmt.Sprintf("错误：当前处于计划模式（只读），已拒绝执行 %s。请只做调研并用文字给出改动方案，等用户退出计划模式后再动手。", name)
	}
	t, ok := tools.Find(name)
	if !ok {
		return fmt.Sprintf("错误：不存在工具 %s。可用工具：%s", name, toolNames())
	}
	args, err := parseArgs(rawArgs)
	if err != nil {
		return "错误：参数不是合法 JSON（已自动尝试修复仍未成功）：" + err.Error()
	}
	// 自定义子代理的工具白名单：越权调用直接拒绝，而不是悄悄跑掉。
	if e.toolAllow != nil && !e.toolAllow[name] {
		return "错误：当前子代理未获授权使用工具 " + name + "；可用：" + e.allowedToolList()
	}
	// 权限策略：deny 一律拒绝，ask 走确认，allow 直接放行。
	preApproved := false
	if e.Policy != nil {
		dec := e.Policy.Decide(name, permTarget(name, args, e.WorkDir),
			!tools.IsReadOnly(name), permRisk(name, args), e.WorkDir)
		switch dec.Kind {
		case "deny":
			return "错误：权限策略拒绝执行 " + name + "（" + dec.Reason + "）。需要放行请让用户用 /perm 调整规则。"
		case "ask":
			if e.Confirm == nil {
				return "错误：" + name + " 需要用户授权，但当前没有可用的确认通道，已拒绝。"
			}
			if !e.Confirm(permBanner("需要授权："+name, dec.Reason), permDetail(name, args)) {
				return "错误：用户拒绝了 " + name + " 的这次调用。不要再重复尝试，改走别的方案或询问用户。"
			}
			preApproved = true
		}
	}
	ctx := &tools.Context{
		PreApproved: preApproved,
		Agents:      core.LoadAgentDefsCached(e.WorkDir),
		WorkDir:     e.WorkDir,
		Confirm: func(title, detail string) bool {
			if e.Confirm == nil {
				return false
			}
			return e.Confirm(title, detail)
		},
		ConfirmDiff: func(title, path string, hunks []core.DiffHunk, allowPartial bool) []int {
			if e.ConfirmDiff == nil {
				// 未注入富差分 UI 时退化为旧的纯文本确认：全接受或全拒绝。
				if e.Confirm == nil {
					return nil
				}
				body := core.UnifiedDiff("", "", path, 3)
				for _, h := range hunks {
					for _, l := range h.Lines {
						body += string(l.Prefix) + l.Text + "\n"
					}
				}
				if e.Confirm(title, body) {
					all := make([]int, len(hunks))
					for i := range all {
						all[i] = i
					}
					return all
				}
				return nil
			}
			return e.ConfirmDiff(title, path, hunks, allowPartial)
		},
		Progress: func(s string) {
			e.emit(Event{Kind: "status", Text: s})
		},
		MaxSubTasks:     e.MaxSubTasks,
		HistoryDir:      core.SessionsDir(),
		SubTaskMaxChars: e.SubTaskMaxChars,
	}
	if e.depth == 0 {
		ctx.Spawn = e.spawnSub
		ctx.SpawnNamed = e.spawnSubWithDef
		ctx.SpawnBatch = e.SpawnBatch
	}
	if block, note := tools.RunHooks(tools.HookPreTool, name, ctx); block {
		if note == "" {
			note = "钩子以非零退出码结束"
		}
		return "错误：pre_tool 钩子阻止了本次执行（" + name + "）：\n" + note
	} else if note != "" {
		e.emit(Event{Kind: "status", Text: "钩子通过：" + name})
	}

	res, err := t.Run(ctx, args)
	if err != nil {
		return "错误：" + err.Error()
	}
	e.touchIndex(name, args)
	// apply_patch 的 dry_run 只预览不写盘，不应触发自动验证。
	if name != "apply_patch" || !tools.GetBool(args, "dry_run", false) {
		e.MarkWrite(name, writePathsForTool(name, args)...)
	}
	// 待办清单与图片理解都靠工具结果驱动侧栏/多模态回灌。
	e.syncBoardFromTool(name, args, res.Output)
	e.collectImages(name, res.Output)
	if name == "read_image" {
		// 载荷已转存到多模态消息，工具结果只留文字，避免 base64 反复进出上下文。
		res.Output = stripImageMarkers(res.Output)
	}
	if res.Output == "" {
		return "（完成，无输出）"
	}
	// 超长输出先本地摘要再回灌：保留头尾与错误/定位行，中间折叠。
	// 直接原样塞回上下文，一次 grep 就能把后面几轮的空间吃光。
	out = tools.SummarizeToolOutput(res.Output, tools.DefaultToolOutputBudget)
	if _, note := tools.RunHooks(tools.HookPostTool, name, ctx); note != "" {
		out += "\n\n" + note
	}
	return out
}

// touchIndex 在写类工具成功后通知代码索引：能定位到具体文件就只重解析该文件，
// 批量补丁/命令执行这类改不清范围的就标记全量重建，保证 code_search 的行号不过期。
func (e *Engine) touchIndex(name string, args map[string]interface{}) {
	switch name {
	case "write_file", "edit_file", "delete_path":
		if p := core.ResolvePath(e.WorkDir, tools.Get(args, "path")); p != "" {
			codeidx.Touch(p)
		}
	case "apply_patch", "run_command":
		codeidx.MarkStale()
	}
}

// addUsage 累计 token 用量。
func (e *Engine) addUsage(u llm.Usage) {
	e.addUsageFor(e.Model, u)
	e.recordCache(u)
}

// Usage 返回累计 token 用量与轮数。
func (e *Engine) Usage() (tokIn, tokOut, turns int) {
	e.usageMu.Lock()
	defer e.usageMu.Unlock()
	return e.tokIn, e.tokOut, e.turns
}

// runTurnEndHooks 每轮结束时执行 turn_end 钩子。
func (e *Engine) runTurnEndHooks() {
	ctx := &tools.Context{
		WorkDir: e.WorkDir,
		Progress: func(s string) {
			e.emit(Event{Kind: "status", Text: s})
		},
	}
	if _, note := tools.RunHooks(tools.HookTurnEnd, "", ctx); note != "" {
		e.emit(Event{Kind: "notice", Text: note})
	}
	e.sinkProjectContract()
}

// sinkProjectContract 在真正改过代码后，把探测到的构建/测试命令等沉淀进
// AGENTS.md 的自动区块（幂等：内容没变不写盘、不打扰用户）。
// 这样下次开新会话，项目约定无需重新摸索。
func (e *Engine) sinkProjectContract() {
	if !e.wroteThisTurnNow() || e.WorkDir == "" {
		return
	}
	path, changed, err := core.UpsertAgentsContract(e.WorkDir)
	if err != nil || !changed {
		return
	}
	e.emit(Event{Kind: "notice", Text: "已把项目约定沉淀到 " + path + "（构建/测试命令、目录结构），下次开新会话会自动带上。"})
}

func toolNames() string {
	var names []string
	for _, t := range tools.All() {
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
}

func httpErrDetail(msg string, code int) string {
	prefix := fmt.Sprintf("HTTP %d:", code)
	if !strings.HasPrefix(msg, prefix) {
		return ""
	}
	d := strings.TrimSpace(strings.TrimPrefix(msg, prefix))
	if r := []rune(d); len(r) > 240 {
		d = string(r[:240]) + "…"
	}
	return d
}

func friendlyErr(err error) string {
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "connection refused"):
		return "连接被拒绝：请确认本地模型服务已启动，端口填写正确（如 http://127.0.0.1:11434/v1）。"
	case strings.Contains(low, "no such host"), strings.Contains(low, "dns"):
		return "域名解析失败：请检查网络或 Base URL 是否拼写正确。"
	case strings.Contains(low, "401"):
		if d := httpErrDetail(msg, 401); d != "" {
			return "鉴权失败（401）：" + d
		}
		return "鉴权失败（401）：API Key 无效或已过期。"
	case strings.Contains(low, "403"):
		if d := httpErrDetail(msg, 403); d != "" {
			return "无权限（403）：" + d
		}
		return "无权限（403）：Key 可能没有该模型权限，或需要在控制台开通。"
	case strings.Contains(low, "404"):
		if d := httpErrDetail(msg, 404); d != "" {
			return "接口不存在（404）：" + d
		}
		return "接口不存在（404）：Base URL 通常需要以 /v1 结尾，或该服务不是 OpenAI 兼容接口。"
	case strings.Contains(low, "429"):
		if d := httpErrDetail(msg, 429); d != "" {
			return "请求过于频繁或余额不足（429）：" + d
		}
		return "请求过于频繁或余额不足（429）：请稍后重试或检查账户额度。"
	case strings.Contains(low, "timeout"), strings.Contains(low, "deadline"):
		return "请求超时：可尝试调小上下文、检查代理设置，或在设置里增大超时时间。"
	case strings.Contains(low, "certificate"), strings.Contains(low, "tls"):
		return "TLS 证书校验失败：Win7 的系统根证书库过旧（停在 2009），本程序内置 22 张根证书兜底；若端点的根仍不在内，请在设置里填 TLS ExtraCA，或让端点换用常见 CA 签发的证书。"
	case strings.Contains(low, "context canceled"):
		return "已取消。"
	case strings.Contains(low, "400"):
		if d := httpErrDetail(msg, 400); d != "" {
			return "请求不合法（400）：" + d
		}
		if strings.Contains(low, "context") || strings.Contains(low, "maximum") || strings.Contains(low, "length") || strings.Contains(low, "token") {
			return "上下文超出模型窗口（400）：请先 /compact 压缩早期对话，或换更大上下文的模型。"
		}
		return "请求不合法（400）：上游拒绝了请求体，常见原因：消息格式异常、模型不支持工具、参数超限。"
	case strings.Contains(low, "insufficient_quota"), strings.Contains(low, "quota"):
		return "配额或余额不足：请检查账号额度（控制台），或与服务商确认套餐。"
	case strings.Contains(low, "model not found"), strings.Contains(low, "does not exist"), strings.Contains(low, "model_not_found"):
		return "模型不存在：请检查模型名是否拼写正确（不同服务商对模型名大小写敏感）。"
	case strings.Contains(low, "500"), strings.Contains(low, "502"), strings.Contains(low, "503"):
		return "上游服务故障（5xx）：服务端暂时不可用，请稍后重试；若持续，请检查服务商公告或换端点。"
	}
	return "请求失败：" + msg
}

// buildContext 组装上下文：系统提示 + 历史消息（按预算裁剪）。
func (e *Engine) buildContext() []llm.Message {
	e.mu.Lock()
	hist := make([]llm.Message, len(e.Messages))
	copy(hist, e.Messages)
	e.mu.Unlock()

	sysText := SystemPrompt(e.WorkDir) + projectDocBlock(e.WorkDir) + skills.Block(e.WorkDir) + memoryBlock(e.WorkDir)
	if e.PlanMode {
		sysText += planModeBlock()
	}
	if extra := strings.TrimSpace(e.SystemExtra); extra != "" {
		sysText += "\n\n## 本次任务的额外要求\n" + extra
	}
	if e.toolAllow != nil {
		sysText += "\n\n（你只能使用以下工具：" + e.allowedToolList() + "；其它工具会直接被拒绝，不要尝试。）"
	}
	sys := llm.Message{Role: "system", Content: sysText}
	out := []llm.Message{sys}

	// 预算：按模型上下文窗口折算（与 compactWater 同一口径），不再写死 120000。
	budget := e.compactWater()
	total := len(sys.Content)
	start := len(hist)
	for i := len(hist) - 1; i >= 0; i-- {
		l := messageSize(&hist[i])
		if total+l > budget && start < len(hist)-2 {
			break
		}
		total += l
		start = i
	}
	// 对齐：不能切出孤儿 tool 消息（协议破坏，OpenAI 兼容端一律 400）。
	// 只需要这一条回退——assistant(tool_calls) 与它的 tool 响应自成一段，
	// 不需要额外的 assistant 回退（它反而是制造孤儿的来源：把 start 往回挪到 tool 上）。
	for start > 0 && hist[start].Role == "tool" {
		start--
	}
	if start > 0 {
		out = append(out, llm.Message{
			Role:    "user",
			Content: fmt.Sprintf("（提示：更早的 %d 条对话因上下文长度限制已省略，如需相关信息请重新读取文件或询问我。）", start),
		})
	}
	out = append(out, hist[start:]...)
	return out
}

// SystemPrompt 生成系统提示词。针对编码 Agent 场景调优。
func SystemPrompt(workDir string) string {
	var env strings.Builder
	fmt.Fprintf(&env, "操作系统: %s %s\n", core.OSVersion(), runtime.GOARCH)
	fmt.Fprintf(&env, "当前日期: %s\n", time.Now().Format("2006-01-02"))
	fmt.Fprintf(&env, "工作目录: %s\n", workDir)
	fmt.Fprintf(&env, "Go 运行时: %s\n", runtime.Version())
	if isWin7Host() {
		fmt.Fprintf(&env, "默认 shell: cmd.exe（PowerShell 2.0 可用）\n")
	} else {
		fmt.Fprintf(&env, "默认 shell: cmd.exe（PowerShell 版本随系统）\n")
	}
	fmt.Fprintf(&env, "数据目录: %s\n", core.LocalDataDir())
	fmt.Fprintf(&env, "全局技能目录: %s\n", skills.GlobalDir())
	fmt.Fprintf(&env, "项目技能目录: %s\n", skills.ProjectDir(workDir))
	fmt.Fprintf(&env, "配置文件: %s\n", core.ConfigPath())
	fmt.Fprintf(&env, "运行日志: %s\n", core.LogPath())
	fmt.Fprintf(&env, "程序目录: %s\n", core.ExeDir())
	env.WriteString(workspaceSummary(workDir))

	return `你是 WIN7 Harness 内置的编程智能体，运行在用户的 Windows 本机上，可以直接读写文件、执行命令、完成软件开发和各类资料处理任务。你的能力对标 Codex / Claude Code / OpenCode 这类终端 Agent。

## 运行环境
` + env.String() + runtimePromptFor(isWin7Host()) + `
## 工作方式
1. 动手前先了解现状：用 list_dir / glob / grep / read_file 查看真实代码与文件，不要凭空猜测文件内容或路径。
2. 多步骤任务先用 todo_write 列出计划，让用户看到进度；每完成一步就更新状态。
3. 修改代码优先用 edit_file 做精确替换，只有新建文件或整体重写时才用 write_file。
   当一次任务需要改动多处或多份文件时，用 apply_patch 把这些改动合并成一次调用（可先 dry_run 预览），比连打十几次 edit_file 更快，也不容易中途失败留半成品。
4. 需要编译、测试、装依赖、跑脚本时用 run_command。习惯把构建产物放在 build/ 或 dist/ 下。
5. 工具返回错误时，先读错误信息定位原因再改，不要重复执行同一条失败命令。
6. 完成后给出简明结论：改了哪些文件、怎么验证、还剩什么风险。不要罗列无关细节。
7. 用户问“需要哪些 / 怎么做 / 有没有问题 / 你看一下”时，先给判断和方案，不要直接新建文件或技能；只有明确说“做 / 改 / 写 / 建”才动手。
8. 不顺手创建用户没要求的 skill、文档或示例；一次只交付当前要求的东西。需要扩大范围时先问一句。
9. 新项目或大型工程：先用 todo_write 给出 3~8 步计划，第一步确认技术栈、目标平台和验收标准；用户确认后再建目录、写代码。不要用 skill/文档充数，也不要在需求没确认时铺开十几个文件。

## 专项工具（按需选用，不要拿通用工具硬凑）
- 新仓库/大仓库先 repo_map 看 module、源码目录、main 入口和测试分布，再决定读哪些文件。
- 改完单文件或提交前用 diagnostics 拿编译器/静态诊断（Go 为 go vet，Rust 为 cargo check），比等全量测试快；IDE 级定义跳转/引用/悬停用 lsp（需安装 gopls/pylsp/rust-analyzer 等）。
- 找符号定义先用 code_search；想知道"谁调用了这个函数"用 calls：查调用点、被调用方与影响面，比全局 grep 准。大仓库不要用 grep 扫全仓。
- CSV / JSON / 表格类数据用 datatable 做过滤、求和、分组、排序，不要把整表读进上下文自己算。
- 用户给的是本地图片时用 read_image：它会回传尺寸并把图片作为多模态内容发给你，看完再回答，不要靠猜。
- 关心测试覆盖率或性能是否回退时用 quality：读 coverprofile 给覆盖短板，读 benchmark 输出与基线比对给回归项。
- 需要同时改多份互不相干的文件时，可以在 task 里拆成多个写子任务，系统会各开隔离副本并行写、最后合并，冲突会另存不覆盖。

## 上下文与回滚
- 工作目录的 AGENTS.md 等项目约定已自动注入本提示，其中的规范必须遵守；与用户当次指令冲突时以用户指令为准。
- 文件写入、修改、删除前程序都会自动快照，用户可用 /undo 回滚；因此不必为每处改动反复请求确认，但不要顺手重写与本任务无关的文件。
- 多个互不依赖的读取操作（read_file / grep / glob / list_dir）应当一次性全部发起，它们会并行执行，比逐个等待快得多。

## 回答风格
- 中文回答，专业、克制、直接给结论；不要寒暄、不要表情符号。
- 涉及代码时使用带语言标记的 Markdown 代码块。
- 不确定的地方明确说明"需要确认"，不要编造 API、路径、命令输出。
- 破坏性操作（删除、覆盖、格式化、改系统配置）必须先说明后果再执行。

## 安全边界
- 不修改 C:\Windows、Program Files 等系统目录。
- 不读取或外传用户的密钥、证书、浏览器密码等敏感数据。
- 用户拒绝的操作不要换个方式偷偷执行。`
}

// planModeBlock 计划模式下的追加提示。
func planModeBlock() string {
	return `

## 当前处于计划模式（只读）
- 只允许 read_file / list_dir / glob / grep / todo_write，任何写入、修改、删除、执行命令都会被系统直接拒绝。
- 你的产出是一份可执行的改动方案：涉及哪些文件、每处改什么、为什么、如何验证。
- 方案给出后请用户确认，由用户执行 /plan 退出计划模式再实施，不要反复尝试被拒绝的工具。`
}

func osName() string {
	if runtime.GOOS == "windows" {
		return "Windows"
	}
	return runtime.GOOS
}

// isWin7Host 用 RtlGetVersion 的真实版本判断，不依赖 GetVersionEx
// （Win8.1+ 会撒谎）。同一套二进制在 Win7/10/11 上因此拿到不同的提示词。
func isWin7Host() bool {
	return runtime.GOOS == "windows" && strings.HasPrefix(core.OSVersion(), "Windows 6.1.")
}

// CompactSummary 生成会话摘要（用于"压缩上下文"按钮）。
func CompactSummary(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case "user":
			b.WriteString("- 用户：" + firstLine(m.Content, 120) + "\n")
		case "assistant":
			if m.Content != "" {
				b.WriteString("- 助手：" + firstLine(m.Content, 120) + "\n")
			}
			for _, tc := range m.ToolCalls {
				b.WriteString("- 调用 " + tc.Function.Name + "\n")
			}
		}
	}
	return b.String()
}

// clipLog 把工具参数压成一行，限制日志体积。
func clipLog(s string, n int) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func firstLine(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

var _ = core.AppName
