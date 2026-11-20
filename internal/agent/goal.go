package agent

import (
	"fmt"
	"strings"
	"sync"
)

// GoalStep 目标拆解后的一步。
type GoalStep struct {
	Desc     string
	Done     bool
	Attempts int
	Note     string
}

// GoalReport 一次目标收敛循环的结果。
type GoalReport struct {
	Goal   string
	Steps  []GoalStep
	Rounds int
	// Stop 停止原因：achieved / max-rounds / stalled / cancelled / no-plan
	Stop string
	// Detail 补充说明（最后一次验证结论等）。
	Detail string
}

// Achieved 是否达成目标。
func (r *GoalReport) Achieved() bool { return r != nil && r.Stop == "achieved" }

// Progress 返回形如 "2/5" 的完成度。
func (r *GoalReport) Progress() string {
	if r == nil {
		return ""
	}
	done := 0
	for _, s := range r.Steps {
		if s.Done {
			done++
		}
	}
	return fmt.Sprintf("%d/%d", done, len(r.Steps))
}

// Summary 一行式结论，供界面与日志复用。
func (r *GoalReport) Summary() string {
	if r == nil {
		return ""
	}
	switch r.Stop {
	case "achieved":
		return "目标已达成（" + r.Progress() + "，共 " + itoa(r.Rounds) + " 轮）"
	case "max-rounds":
		return "已达轮数上限，未通过验证（" + r.Progress() + "）"
	case "stalled":
		return "连续多轮无进展，已中止（" + r.Progress() + "）"
	case "cancelled":
		return "已被用户停止（" + r.Progress() + "）"
	case "no-plan":
		return "无法拆解出可执行的步骤"
	}
	return "已结束（" + r.Progress() + "）"
}

// GoalRunner 驱动"拆解 → 逐步执行 → 整体验证 → 不达标再来一轮"的收敛循环。
// 纯逻辑、无 IO，plan / exec / verify 全部由调用方注入，便于单测。
type GoalRunner struct {
	Goal string
	// Plan 把目标拆成有序步骤。
	Plan func(goal string) ([]string, error)
	// Exec 执行一步，返回该步结论（失败用 error 表示）。
	Exec func(step string, round int) (string, error)
	// Verify 做整体验收：返回是否达标 + 结论文本。
	Verify func(round int) (bool, string)
	// MaxRounds 最大循环轮数，0 用默认 6。
	MaxRounds int
	// StallLimit 连续无进展轮数上限，0 用默认 2。
	StallLimit int
	// OnEvent 过程回调（进度展示用，可为 nil）。
	OnEvent func(string)
	// Cancelled 轮次之间检查是否已被用户停止，可为 nil。
	Cancelled func() bool
}

// Run 执行收敛循环。
func (r *GoalRunner) Run() *GoalReport {
	rep := &GoalReport{Goal: r.Goal}
	emit := func(s string) {
		if r.OnEvent != nil {
			r.OnEvent(s)
		}
	}
	steps, err := r.Plan(r.Goal)
	if err != nil || len(steps) == 0 {
		rep.Stop = "no-plan"
		if err != nil {
			rep.Detail = err.Error()
		}
		return rep
	}
	for _, s := range steps {
		rep.Steps = append(rep.Steps, GoalStep{Desc: s})
	}
	maxRounds := r.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 6
	}
	stallLimit := r.StallLimit
	if stallLimit <= 0 {
		stallLimit = 2
	}
	stall := 0
	for round := 1; round <= maxRounds; round++ {
		if r.Cancelled != nil && r.Cancelled() {
			rep.Stop = "cancelled"
			return rep
		}
		rep.Rounds = round
		progressed := false
		for i := range rep.Steps {
			st := &rep.Steps[i]
			if st.Done {
				continue
			}
			if r.Cancelled != nil && r.Cancelled() {
				rep.Stop = "cancelled"
				return rep
			}
			st.Attempts++
			emit(fmt.Sprintf("第 %d 轮 · 第 %d/%d 步：%s", round, i+1, len(rep.Steps), st.Desc))
			note, err := r.Exec(st.Desc, round)
			st.Note = truncateRunes(note, 300)
			if err != nil {
				// 单步失败不直接终止：留给下一轮重试或整体验证兜底。
				continue
			}
			st.Done = true
			progressed = true
		}
		if r.Verify != nil {
			ok, detail := r.Verify(round)
			rep.Detail = detail
			if ok {
				for i := range rep.Steps {
					rep.Steps[i].Done = true
				}
				rep.Stop = "achieved"
				return rep
			}
			emit("整体验证未通过：" + truncateRunes(detail, 200))
		} else if allDone(rep.Steps) {
			rep.Stop = "achieved"
			return rep
		}
		if progressed {
			stall = 0
		} else {
			stall++
			if stall >= stallLimit {
				rep.Stop = "stalled"
				return rep
			}
		}
	}
	rep.Stop = "max-rounds"
	return rep
}

func allDone(steps []GoalStep) bool {
	for _, s := range steps {
		if !s.Done {
			return false
		}
	}
	return len(steps) > 0
}

// truncateRunes 按字符截断，避免把多字节字符切坏。
func truncateRunes(s string, n int) string {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) <= n {
		return string(rs)
	}
	return string(rs[:n]) + "…"
}

// TodoItem 任务清单的一项（来自 todo_write）。
type TodoItem struct {
	Text   string
	Status string // pending / doing / done
}

// TaskBoard 是给"任务侧栏"用的实时任务视图。
// 引擎在跑工具、派子任务、跑目标循环时更新，界面只读快照。
type TaskBoard struct {
	mu      sync.RWMutex
	todos   []TodoItem
	goal    *GoalReport
	goalRun string // 当前正在推进的目标（循环进行中时非空）
	running int    // 正在跑的子任务数
	phase   string // 当前动作的一句话描述
}

// NewTaskBoard 创建空看板。
func NewTaskBoard() *TaskBoard { return &TaskBoard{} }

// SetTodos 整体替换任务清单。
func (b *TaskBoard) SetTodos(items []TodoItem) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.todos = items
	b.mu.Unlock()
}

// SetGoal 记录一次目标循环的最终结果。
func (b *TaskBoard) SetGoal(rep *GoalReport) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.goal = rep
	b.goalRun = ""
	b.mu.Unlock()
}

// SetGoalRunning 标记目标循环开始 / 结束。
func (b *TaskBoard) SetGoalRunning(goal string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.goalRun = goal
	if goal == "" {
		b.phase = ""
	}
	b.mu.Unlock()
}

// SetPhase 记录当前正在做的事。
func (b *TaskBoard) SetPhase(s string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.phase = s
	b.mu.Unlock()
}

// SubStart / SubDone 维护在跑的子任务计数。
func (b *TaskBoard) SubStart() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.running++
	b.mu.Unlock()
}

// SubDone 结束一个子任务。
func (b *TaskBoard) SubDone() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.running > 0 {
		b.running--
	}
	b.mu.Unlock()
}

// TaskSnapshot 是看板的只读快照。
type TaskSnapshot struct {
	Todos     []TodoItem
	Goal      *GoalReport
	GoalRun   string
	Running   int
	Phase     string
	DoneCount int
	AllCount  int
}

// Snapshot 取一份快照。
func (b *TaskBoard) Snapshot() TaskSnapshot {
	if b == nil {
		return TaskSnapshot{}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	s := TaskSnapshot{
		Todos:   append([]TodoItem(nil), b.todos...),
		Goal:    b.goal,
		GoalRun: b.goalRun,
		Running: b.running,
		Phase:   b.phase,
	}
	for _, t := range s.Todos {
		s.AllCount++
		if t.Status == "done" {
			s.DoneCount++
		}
	}
	return s
}

// Empty 看板是否没有任何可展示内容。
func (s TaskSnapshot) Empty() bool {
	return len(s.Todos) == 0 && s.Goal == nil && s.GoalRun == "" && s.Running == 0 && s.Phase == ""
}
