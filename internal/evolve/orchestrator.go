package evolve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"win7harness/internal/eval"
)

// Run 执行一轮自迭代。Stage 0 默认只把候选合入影子分支（TargetBranch），
// 不直接动 BaseBranch；合并目标由调用方通过 TargetBranch 决定。
type runState struct {
	ctx        context.Context
	cfg        Config
	start      time.Time
	runID      string
	repo       string
	workRoot   string
	outDir     string
	rep        *RunReport
	logBuf     strings.Builder
	targetDir  string
	keepTarget bool
	baseEval   eval.Report
	tokens     int
	propose    func() (ProposeResult, error)
}

func (s *runState) logf(format string, args ...interface{}) {
	line := fmt.Sprintf(format, args...)
	s.logBuf.WriteString(line + "\n")
	if s.cfg.Logf != nil {
		s.cfg.Logf("%s", line)
	}
}

func (s *runState) save() {
	s.rep.EndedAt = s.cfg.now().Format(time.RFC3339)
	s.rep.DurationMs = s.cfg.now().Sub(s.start).Milliseconds()
	if err := SaveRun(s.outDir, s.rep); err != nil {
		s.logf("写报告失败：%v", err)
	}
	_ = os.WriteFile(filepath.Join(s.outDir, "run.log"), []byte(s.logBuf.String()), 0o644)
}

func (s *runState) fail(stop string, err error) (*RunReport, error) {
	s.rep.Stop = stop
	s.rep.StopDetail = err.Error()
	s.logf("停止：%s（%v）", stop, err)
	s.save()
	return s.rep, err
}

func Run(ctx context.Context, cfg Config) (*RunReport, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	start := cfg.now()
	runID := cfg.RunID
	if runID == "" {
		runID = start.Format("20060102-150405")
	}
	repo, _ := filepath.Abs(cfg.Repo)
	workRoot, _ := filepath.Abs(cfg.WorkRoot)
	// 基线默认用当前分支：这个仓库叫 master，别的仓库可能叫 main，
	// 写死任何一个都会在另一边第一步就失败。
	if strings.TrimSpace(cfg.BaseBranch) == "" {
		if b, err := gitOut(ctx, repo, "branch", "--show-current"); err == nil && strings.TrimSpace(b) != "" {
			cfg.BaseBranch = strings.TrimSpace(b)
		}
	}
	if strings.TrimSpace(cfg.BaseBranch) == "" {
		return nil, fmt.Errorf("无法确定基线分支，请用 -base 指定")
	}
	outDir := filepath.Join(repo, "rsi-out", runID)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建报告目录失败：%w", err)
	}

	s := &runState{ctx: ctx, cfg: cfg, start: start, runID: runID, repo: repo, workRoot: workRoot, outDir: outDir}
	// 工作树清理放在 Run 的 defer 里：preflight 返回时不能删，否则后续阶段没有工作树可用。
	defer func() {
		if s.targetDir != "" && !s.keepTarget {
			removeWorktree(ctx, repo, s.targetDir)
		}
	}()
	rep := &RunReport{
		ID:           runID,
		StartedAt:    start.Format(time.RFC3339),
		Repo:         repo,
		BaseBranch:   cfg.BaseBranch,
		TargetBranch: cfg.TargetBranch,
		DryRun:       cfg.DryRun,
	}
	s.rep = rep
	rep, err := s.preflight()
	if err != nil {
		return rep, err
	}
	if s.cfg.DryRun {
		return s.runDryRun()
	}
	return s.runCandidates()
}

// preflight 完成 git 与工作树检查。
func (s *runState) preflight() (*RunReport, error) {
	if !isGitRepo(s.ctx, s.repo) {
		return s.fail("preflight-error", fmt.Errorf("%s 不是 git 仓库", s.repo))
	}
	if !s.cfg.AllowDirty {
		st, err := statusPorcelain(s.ctx, s.repo)
		if err != nil {
			return s.fail("preflight-error", err)
		}
		if strings.TrimSpace(st) != "" {
			return s.fail("preflight-error", fmt.Errorf("主工作区有未提交改动，先收口再开自迭代：\n%s", clip(st, 800)))
		}
	}
	if err := ensureBranch(s.ctx, s.repo, s.cfg.TargetBranch, s.cfg.BaseBranch); err != nil {
		return s.fail("preflight-error", err)
	}
	targetDir, err := prepareWorktree(s.ctx, s.repo, s.workRoot, "target", s.cfg.TargetBranch, false, "")
	if err != nil {
		return s.fail("preflight-error", err)
	}
	s.targetDir = targetDir

	s.logf("=== evolve %s ===", s.runID)
	s.logf("repo=%s", s.repo)
	s.logf("base=%s target=%s", s.cfg.BaseBranch, s.cfg.TargetBranch)
	if err := updateTarget(s.ctx, s.targetDir, s.cfg.BaseBranch); err != nil {
		return s.fail("preflight-error", err)
	}

	if s.cfg.Eval != nil {
		s.baseEval, err = s.cfg.Eval(s.ctx, s.targetDir)
		if err != nil {
			if !s.cfg.DryRun {
				return s.fail("preflight-error", fmt.Errorf("基线评测失败：%w", err))
			}
			s.logf("基线评测失败（dry-run 忽略）：%v", err)
		} else {
			s.rep.BaseEval = s.baseEval
			s.logf("基线评测：通过 %d / 失败 %d / 跳过 %d", s.baseEval.Pass, s.baseEval.Fail, s.baseEval.Skip)
		}
	}

	signals := CollectSignals(s.ctx, s.targetDir)
	history := LoadHistory(s.repo, 20)
	if len(signals) == 0 {
		signals = append(signals, "没有显式信号；请从代码质量、测试覆盖或已知小问题里找低风险改进。")
	}

	s.propose = func() (ProposeResult, error) {
		return s.cfg.Propose(s.ctx, ProposeInput{
			Repo:    s.repo,
			BaseDir: s.targetDir,
			Goal:    s.cfg.Goal,
			Signals: signals,
			History: history,
		})
	}
	s.tokens = 0
	return nil, nil
}

// runDryRun 只生成提案，不执行。
func (s *runState) runDryRun() (*RunReport, error) {
	pr, err := s.propose()
	if err != nil {
		return s.fail("propose-error", err)
	}
	s.tokens += pr.Tokens
	s.rep.Tokens = s.tokens
	s.rep.Proposals = pr.Proposals
	s.rep.Stop = "dry-run"
	s.logf("dry-run：收到 %d 个提案，不执行", len(pr.Proposals))
	for i, p := range pr.Proposals {
		s.logf("  [%d] [%s] %s", i+1, p.Risk, p.Title)
	}
	s.save()
	return s.rep, nil
}

// runCandidates 执行候选的提案、实现、门禁、合并循环。
func (s *runState) runCandidates() (*RunReport, error) {
	consecutiveFailures := 0
	seen := map[string]bool{}
	stop := ""
	stopDetail := ""
	record := func(exp Experiment) {
		s.rep.Experiments = append(s.rep.Experiments, exp)
		if err := AppendHistory(s.repo, exp); err != nil {
			s.logf("写实验历史失败：%v", err)
		}
		s.save()
	}
	failed := func(exp *Experiment, status, reason string) {
		exp.Status = status
		exp.Reason = reason
		exp.EndedAt = s.cfg.now().Format(time.RFC3339)
		consecutiveFailures++
		s.logf("[%s] %s：%s", exp.ID, status, clip(reason, 300))
	}

	for i := 0; i < s.cfg.MaxCandidates; i++ {
		if s.ctx.Err() != nil {
			stop, stopDetail = "cancelled", s.ctx.Err().Error()
			break
		}
		if s.cfg.MaxWallClock > 0 && s.cfg.now().Sub(s.start) >= s.cfg.MaxWallClock {
			stop, stopDetail = "max-wall-clock", "达到本轮时间上限"
			break
		}
		if s.cfg.MaxTokens > 0 && s.tokens >= s.cfg.MaxTokens {
			stop, stopDetail = "max-s.tokens", "达到本轮 token 上限"
			break
		}

		pr, err := s.propose()
		if err != nil {
			stop, stopDetail = "propose-error", err.Error()
			break
		}
		s.tokens += pr.Tokens
		proposals := pr.Proposals
		if len(proposals) == 0 {
			stop, stopDetail = "no-proposals", "提案器没有给出可执行提案"
			break
		}
		s.rep.Proposals = append(s.rep.Proposals, proposals...)
		var p Proposal
		found := false
		for _, cand := range proposals {
			key := strings.ToLower(strings.TrimSpace(cand.Title))
			if key == "" || seen[key] {
				continue
			}
			p = cand
			seen[key] = true
			found = true
			break
		}
		if !found {
			stop, stopDetail = "no-new-proposals", "提案器给出的都是本轮已经试过的提案"
			break
		}
		exp := Experiment{
			ID:        fmt.Sprintf("%s-c%d", s.runID, i+1),
			Title:     p.Title,
			Risk:      p.Risk,
			Status:    StatusProposed,
			Proposal:  p,
			StartedAt: s.cfg.now().Format(time.RFC3339),
		}
		s.logf("[%s] 提案 [%s] %s", exp.ID, p.Risk, p.Title)

		if p.Risk == RiskC {
			exp.Status = StatusSkipped
			exp.Reason = "风险 C：不自动合并，留给人工"
			exp.EndedAt = s.cfg.now().Format(time.RFC3339)
			s.logf("[%s] 跳过（风险 C）", exp.ID)
			record(exp)
			consecutiveFailures++
			if consecutiveFailures >= 2 {
				stop, stopDetail = "stalled", "连续两次提案被跳过/失败"
				break
			}
			continue
		}

		candBranch := "rsi/cand-" + exp.ID
		candDir, err := prepareWorktree(s.ctx, s.repo, s.workRoot, "cand-"+exp.ID, candBranch, true, s.cfg.TargetBranch)
		if err != nil {
			failed(&exp, StatusError, err.Error())
			record(exp)
			if consecutiveFailures >= 2 {
				stop, stopDetail = "stalled", "连续两次候选失败"
				break
			}
			continue
		}
		cleanupCand := func() {
			removeWorktree(s.ctx, s.repo, candDir)
			deleteBranch(s.ctx, s.repo, candBranch)
		}

		t0 := s.cfg.now()
		impl, err := s.cfg.Implement(s.ctx, ImplementInput{
			Repo:      s.repo,
			BaseDir:   s.targetDir,
			WorkDir:   candDir,
			Proposal:  p,
			MaxRounds: s.cfg.MaxRounds,
		})
		exp.Tokens = impl.Tokens
		exp.Summary = impl.Summary
		s.tokens += impl.Tokens
		exp.DurationMs = s.cfg.now().Sub(t0).Milliseconds()
		if err != nil {
			if p := captureWorktreePatch(s.ctx, candDir); p != "" {
				_ = WritePatch(s.outDir, exp.ID, p)
			}
			failed(&exp, StatusImplFailed, err.Error())
			cleanupCand()
			record(exp)
			if consecutiveFailures >= 2 {
				stop, stopDetail = "stalled", "连续两次候选失败"
				break
			}
			continue
		}

		commit, err := commitAll(s.ctx, candDir, "rsi: "+p.Title)
		if err != nil {
			if err == errNoChanges {
				failed(&exp, StatusNoChanges, "实现器没有产生文件改动")
			} else {
				failed(&exp, StatusError, err.Error())
			}
			cleanupCand()
			record(exp)
			if consecutiveFailures >= 2 {
				stop, stopDetail = "stalled", "连续两次候选失败"
				break
			}
			continue
		}
		exp.Commit = commit

		if d, err := diffStat(s.ctx, s.repo, s.cfg.TargetBranch, candBranch); err == nil {
			exp.Diff = d
			s.logf("[%s] 改动：%d 个文件 +%d/-%d", exp.ID, d.Files, d.Added, d.Deleted)
		}
		if patch, err := gitOut(s.ctx, s.repo, "diff", s.cfg.TargetBranch+"..."+candBranch); err == nil {
			_ = WritePatch(s.outDir, exp.ID, patch)
		}

		gate, err := s.cfg.Gate(s.ctx, GateInput{
			BaseDir:    s.targetDir,
			CandDir:    candDir,
			BaseBranch: s.cfg.TargetBranch,
			CandBranch: candBranch,
			BaseEval:   s.baseEval,
		})
		exp.Gate = &gate
		if err != nil {
			failed(&exp, StatusError, "门禁无法执行："+err.Error())
			cleanupCand()
			record(exp)
			if consecutiveFailures >= 2 {
				stop, stopDetail = "stalled", "连续两次候选失败"
				break
			}
			continue
		}
		if !gate.OK {
			failed(&exp, StatusGateFailed, strings.Join(gate.Failures, "；"))
			cleanupCand()
			record(exp)
			if consecutiveFailures >= 2 {
				stop, stopDetail = "stalled", "连续两次候选失败"
				break
			}
			continue
		}
		s.logf("[%s] 门禁通过", exp.ID)

		merged, err := mergeCandidate(s.ctx, s.targetDir, candBranch, "rsi: merge "+exp.ID+" "+p.Title)
		if err != nil {
			failed(&exp, StatusMergeFail, err.Error())
			cleanupCand()
			record(exp)
			if consecutiveFailures >= 2 {
				stop, stopDetail = "stalled", "连续两次候选失败"
				break
			}
			continue
		}
		exp.MergedCommit = merged

		// 单线程循环里目标分支在候选期间不会前进，合并结果树通常与候选树完全一致。
		// 这种情况下 pre-merge 门禁已经覆盖了同一个树，再跑一遍全量测试只是重复烧时间。
		// 一旦目标分支确实前进了（树不一致），仍然跑 post-merge 门禁并保留自动回滚。
		if candTree, e1 := gitOut(s.ctx, s.repo, "rev-parse", exp.Commit+"^{tree}"); e1 == nil {
			if targetTree, e2 := gitOut(s.ctx, s.targetDir, "rev-parse", "HEAD^{tree}"); e2 == nil && candTree == targetTree {
				exp.Status = StatusMerged
				exp.Reason = "门禁通过，合并后树与候选一致（跳过重复门禁）"
				exp.EndedAt = s.cfg.now().Format(time.RFC3339)
				s.baseEval = gate.Eval
				consecutiveFailures = 0
				s.logf("[%s] 已合入 %s（%s，树一致）", exp.ID, s.cfg.TargetBranch, shortHash(merged))
				cleanupCand()
				record(exp)
				continue
			}
		}

		post, err := s.cfg.Gate(s.ctx, GateInput{
			BaseDir:    s.targetDir,
			CandDir:    s.targetDir,
			BaseBranch: s.cfg.TargetBranch,
			CandBranch: s.cfg.TargetBranch,
			BaseEval:   gate.Eval,
			PostMerge:  true,
		})
		exp.Gate = &post
		if err != nil || !post.OK {
			reason := "合并后门禁失败"
			if err != nil {
				reason += "：" + err.Error()
			} else {
				reason += "：" + strings.Join(post.Failures, "；")
			}
			if rev, rerr := revertLastMerge(s.ctx, s.targetDir); rerr != nil {
				s.keepTarget = true
				reason += "；回滚也失败：" + rerr.Error()
			} else {
				reason += "；已回滚 " + rev
			}
			failed(&exp, StatusReverted, reason)
			cleanupCand()
			record(exp)
			if consecutiveFailures >= 2 {
				stop, stopDetail = "stalled", "连续两次候选失败"
				break
			}
			continue
		}

		exp.Status = StatusMerged
		exp.Reason = "门禁通过，已合入 " + s.cfg.TargetBranch
		exp.EndedAt = s.cfg.now().Format(time.RFC3339)
		s.baseEval = gate.Eval
		consecutiveFailures = 0
		s.logf("[%s] 已合入 %s（%s）", exp.ID, s.cfg.TargetBranch, shortHash(merged))
		cleanupCand()
		record(exp)
	}
	return s.finish(stop, stopDetail)
}

// finish 写入本轮停止原因与统计。
func (s *runState) finish(stop, stopDetail string) (*RunReport, error) {
	if stop == "" {
		stop = "max-candidates"
		stopDetail = fmt.Sprintf("达到本轮候选上限 %d", s.cfg.MaxCandidates)
	}
	s.rep.Stop = stop
	s.rep.StopDetail = stopDetail
	s.rep.Tokens = s.tokens
	s.save()
	s.logf("=== 结束：%s（%s），候选 %d 个 ===", stop, stopDetail, len(s.rep.Experiments))
	return s.rep, nil
}
func (c *Config) validate() error {
	if strings.TrimSpace(c.Repo) == "" {
		return fmt.Errorf("缺少 repo")
	}
	if c.Propose == nil {
		return fmt.Errorf("缺少提案器")
	}
	if !c.DryRun {
		if c.Implement == nil {
			return fmt.Errorf("缺少实现器")
		}
		if c.Gate == nil {
			return fmt.Errorf("缺少门禁")
		}
	}
	if c.MaxCandidates <= 0 {
		c.MaxCandidates = 2
	}
	if c.MaxRounds <= 0 {
		// 实测 24 轮仍会在“读代码 + 改文件 + 定向测试 + 收尾”后撞顶，
		// 再留一点余量给失败重试。
		c.MaxRounds = 32
	}
	if c.MaxDiffLines <= 0 {
		c.MaxDiffLines = 600
	}
	if c.MaxFiles <= 0 {
		c.MaxFiles = 12
	}
	if c.MaxWallClock <= 0 {
		c.MaxWallClock = 3 * time.Hour
	}
	if c.WorkRoot == "" {
		c.WorkRoot = filepath.Join(filepath.Dir(c.Repo), "win7harness-rsi")
	}
	if c.TargetBranch == "" {
		c.TargetBranch = "rsi/auto"
	}
	return nil
}

func shortHash(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
