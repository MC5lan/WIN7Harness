package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/llm"
)

// Board 返回任务看板（懒初始化），界面与命令共用同一份实时视图。
func (e *Engine) Board() *TaskBoard {
	e.boardOnce.Do(func() { e.board = NewTaskBoard() })
	return e.board
}

// RunGoal 以"拆解 → 逐步执行 → 整体验证"的收敛循环推进一个目标，
// 直到验证通过、轮数用尽或连续无进展。
func (e *Engine) RunGoal(ctx context.Context, goal string) *GoalReport {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return &GoalReport{Stop: "no-plan", Goal: goal, Detail: "目标为空"}
	}
	board := e.Board()
	board.SetGoalRunning(goal)
	defer board.SetGoalRunning("")

	e.emit(Event{Kind: "status", Text: "目标收敛：正在拆解目标…"})
	runner := &GoalRunner{
		Goal: goal,
		Plan: func(g string) ([]string, error) {
			return e.planGoalSteps(ctx, g)
		},
		Exec: func(step string, round int) (string, error) {
			board.SetPhase(step)
			defer board.SetPhase("")
			prompt := goalStepPrompt(goal, step, round)
			return e.spawnSub(prompt, "目标第 "+itoa(round)+" 轮："+step, false)
		},
		Verify: func(round int) (bool, string) {
			if !e.verifyEnabled() {
				return false, ""
			}
			e.emit(Event{Kind: "status", Text: "目标收敛：整体验证中…"})
			res := e.runVerify(ctx)
			if res.Skipped != "" {
				// 验证跑不起来时不当作失败，交给轮数与进展判定。
				return false, "验证未执行：" + res.Skipped
			}
			return res.OK, res.VerifySummary()
		},
		OnEvent: func(s string) {
			e.emit(Event{Kind: "status", Text: s})
		},
		Cancelled: func() bool {
			select {
			case <-ctx.Done():
				return true
			default:
				return false
			}
		},
	}
	rep := runner.Run()
	if rep == nil {
		rep = &GoalReport{Goal: goal, Stop: "no-plan"}
	}
	board.SetGoal(rep)
	e.emit(Event{Kind: "notice", Text: "目标收敛：" + rep.Summary()})
	return rep
}

// planGoalSteps 让模型把目标拆成 3~7 个可独立执行、可验收的步骤。
func (e *Engine) planGoalSteps(ctx context.Context, goal string) ([]string, error) {
	if e.Client == nil {
		return nil, fmt.Errorf("未配置模型客户端，无法拆解目标")
	}
	sys := "你是任务规划器。把用户目标拆成 3-7 个可独立执行、且能通过构建/测试或事实验收的步骤。" +
		"只输出 JSON 数组，元素是字符串，不要任何解释、不要 Markdown 代码块。"
	msgs := []llm.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: "目标：" + goal},
	}
	var b strings.Builder
	// 拆目标也是一次真实的模型调用：记账，否则成本看板与预算护栏看不见这笔钱。
	usage, err := e.Client.Chat(ctx, msgs, nil, 0.2, 800, llm.Handlers{
		OnContent: func(s string) { b.WriteString(s) },
	})
	e.addUsageFor(e.Model, usage)
	if err != nil {
		return nil, err
	}
	return parsePlanSteps(b.String())
}

// parsePlanSteps 从模型输出里抠出步骤数组，容忍代码块与前后缀说明。
func parsePlanSteps(raw string) ([]string, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "["); i >= 0 {
		if j := strings.LastIndex(s, "]"); j > i {
			s = s[i : j+1]
		}
	}
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		// 退化为按行拆分：模型偶尔会输出 1. xxx 这样的列表。
		var out []string
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimLeft(line, "-*0123456789.、) ")
			if line != "" && !strings.HasPrefix(line, "```") {
				out = append(out, line)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("无法解析步骤清单：%v", err)
		}
		return out, nil
	}
	var out []string
	for _, s := range arr {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("模型未给出有效步骤")
	}
	return out, nil
}

func goalStepPrompt(goal, step string, round int) string {
	return "【目标收敛循环】整体目标：" + goal + "\n" +
		"当前第 " + itoa(round) + " 轮，本次只做这一步：" + step + "\n" +
		"要求：直接动手完成这一步并给出可核对的结果；不要越界去做其它步骤；" +
		"若这一步涉及改代码，改完请自检编译或测试是否通过。"
}

// RunGoalAsync 在后台跑一次目标收敛循环；已有任务在跑时返回 false。
// 结束时补一条 turn_end 事件，界面据此解除"生成中"状态。
func (e *Engine) RunGoalAsync(goal string) bool {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return false
	}
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.running = true
	e.cancel = cancel
	e.mu.Unlock()

	core.SafeGo("agent/goal_engine.go:155", func() {
		defer func() {
			e.mu.Lock()
			e.running = false
			e.cancel = nil
			e.mu.Unlock()
			e.emit(Event{Kind: "turn_end"})
		}()
		rep := e.RunGoal(ctx, goal)
		e.mu.Lock()
		e.Messages = append(e.Messages, llm.Message{Role: "assistant",
			Content: "【目标收敛】" + goal + "\n" + rep.Summary()})
		e.mu.Unlock()
	})
	return true
}

// LastGoal 返回最近一次目标收敛结果（供 /goal 不带参数时回看）。
func (e *Engine) LastGoal() *GoalReport {
	return e.Board().Snapshot().Goal
}
