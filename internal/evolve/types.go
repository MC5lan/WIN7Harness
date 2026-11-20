// Package evolve 实现受控自迭代（RSI-lite）的编排层：
// 收集信号 → 生成提案 → 隔离实现 → 可信门禁 → 合并/回滚 → 记录实验。
//
// 这个包只做编排与判定，不直接调用模型；提案、实现、门禁都由调用方注入，
// 便于单测，也便于把危险动作关在独立进程里。
package evolve

import (
	"context"
	"time"

	"win7harness/internal/eval"
)

// 实验状态。字符串会写进 JSON，保持稳定。
const (
	StatusProposed   = "proposed"
	StatusSkipped    = "skipped"
	StatusNoChanges  = "no-changes"
	StatusImplFailed = "implement-failed"
	StatusGateFailed = "gate-failed"
	StatusMergeFail  = "merge-failed"
	StatusMerged     = "merged"
	StatusReverted   = "reverted"
	StatusError      = "error"
)

// 风险分级：
//   - A：测试、文档、无行为变化的内部重构，可自动合并；
//   - B：修 bug、性能、错误信息，需要更严的门禁；
//   - C：eval/门禁/权限/存储/更新器/Win32/依赖/构建发布，永不自动合并。
//
// 当前 Stage 0 只把候选合并到影子分支，C 类仍会被跳过并记录。
const (
	RiskA = "A"
	RiskB = "B"
	RiskC = "C"
)

// Proposal 是一个自迭代提案。字段保持简单，方便模型输出 JSON。
type Proposal struct {
	Title          string   `json:"title"`
	Hypothesis     string   `json:"hypothesis,omitempty"`
	Files          []string `json:"files,omitempty"`
	Change         string   `json:"change"`
	Acceptance     string   `json:"acceptance"`
	Risk           string   `json:"risk,omitempty"`
	ExpectedMetric string   `json:"expected_metric,omitempty"`
}

// ProposeInput 是提案器的输入。
type ProposeInput struct {
	Repo    string
	BaseDir string
	Goal    string
	Signals []string
	History []Experiment
}

// ImplementInput 是实现器的输入。WorkDir 是隔离后的候选 worktree。
type ImplementInput struct {
	Repo      string
	BaseDir   string
	WorkDir   string
	Proposal  Proposal
	MaxRounds int
}

// ImplementResult 是实现器的输出。
type ImplementResult struct {
	Summary string
	Tokens  int
}

// DiffStat 是候选相对基线的改动规模。
type DiffStat struct {
	Files   int `json:"files"`
	Added   int `json:"added"`
	Deleted int `json:"deleted"`
}

// Lines 返回增删行数之和。
func (d DiffStat) Lines() int { return d.Added + d.Deleted }

// GateStep 是门禁中的一步。
type GateStep struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Ms     int64  `json:"ms"`
}

// GateResult 是一次门禁的完整结果。
type GateResult struct {
	OK       bool        `json:"ok"`
	Steps    []GateStep  `json:"steps"`
	Eval     eval.Report `json:"eval"`
	BaseEval eval.Report `json:"base_eval"`
	Diff     DiffStat    `json:"diff"`
	Failures []string    `json:"failures,omitempty"`
}

// Experiment 是一次候选的完整记录。
type Experiment struct {
	ID           string      `json:"id"`
	Title        string      `json:"title"`
	Risk         string      `json:"risk"`
	Status       string      `json:"status"`
	Reason       string      `json:"reason,omitempty"`
	Branch       string      `json:"branch,omitempty"`
	Commit       string      `json:"commit,omitempty"`
	MergedCommit string      `json:"merged_commit,omitempty"`
	Proposal     Proposal    `json:"proposal"`
	Summary      string      `json:"summary,omitempty"`
	Tokens       int         `json:"tokens,omitempty"`
	DurationMs   int64       `json:"duration_ms,omitempty"`
	Diff         DiffStat    `json:"diff,omitempty"`
	Gate         *GateResult `json:"gate,omitempty"`
	StartedAt    string      `json:"started_at,omitempty"`
	EndedAt      string      `json:"ended_at,omitempty"`
}

// RunReport 是一轮自迭代的总结。
type RunReport struct {
	ID           string       `json:"id"`
	StartedAt    string       `json:"started_at"`
	EndedAt      string       `json:"ended_at"`
	Repo         string       `json:"repo"`
	BaseBranch   string       `json:"base_branch"`
	TargetBranch string       `json:"target_branch"`
	DryRun       bool         `json:"dry_run"`
	Stop         string       `json:"stop"`
	StopDetail   string       `json:"stop_detail,omitempty"`
	BaseEval     eval.Report  `json:"base_eval"`
	Proposals    []Proposal   `json:"proposals,omitempty"`
	Experiments  []Experiment `json:"experiments"`
	Tokens       int          `json:"tokens"`
	DurationMs   int64        `json:"duration_ms"`
}

// ProposeResult 是提案器的输出；Tokens 用于把提案成本也算进本轮预算。
type ProposeResult struct {
	Proposals []Proposal
	Tokens    int
}

// 注入函数类型。

type ProposeFunc func(ctx context.Context, in ProposeInput) (ProposeResult, error)
type ImplementFunc func(ctx context.Context, in ImplementInput) (ImplementResult, error)

// GateInput 把基线与候选放在一起交给门禁。
// BaseEval 是基线评测结果；门禁用它做逐用例退步判定。
type GateInput struct {
	BaseDir    string
	CandDir    string
	BaseBranch string
	CandBranch string
	BaseEval   eval.Report
	// PostMerge 为 true 时表示候选已经合入目标分支，门禁只做合并后的
	// 构建/测试/评测，不再做受保护路径与 diff 检查。
	PostMerge bool
}

type GateFunc func(ctx context.Context, in GateInput) (GateResult, error)
type EvalFunc func(ctx context.Context, dir string) (eval.Report, error)

// Config 是一轮自迭代的配置。
type Config struct {
	Repo         string
	WorkRoot     string
	BaseBranch   string
	TargetBranch string
	RunID        string
	// AllowDirty 允许主工作区有未提交改动。默认 false：自迭代的基线必须
	// 来自提交，否则候选 diff 会掺进别人的在途修改。
	AllowDirty    bool
	MaxCandidates int
	MaxRounds     int
	MaxDiffLines  int
	MaxFiles      int
	MaxTokens     int
	MaxWallClock  time.Duration
	DryRun        bool
	Goal          string

	Propose   ProposeFunc
	Implement ImplementFunc
	Gate      GateFunc
	Eval      EvalFunc
	Logf      func(format string, args ...interface{})
	Now       func() time.Time
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c Config) logf(format string, args ...interface{}) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}
