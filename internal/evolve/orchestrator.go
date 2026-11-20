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

	rep := &RunReport{
		ID:           runID,
		StartedAt:    start.Format(time.RFC3339),
		Repo:         repo,
		BaseBranch:   cfg.BaseBranch,
		TargetBranch: cfg.TargetBranch,
		DryRun:       cfg.DryRun,
	}
	var logBuf strings.Builder
	logf := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		logBuf.WriteString(line + "\n")
		if cfg.Logf != nil {
			cfg.Logf("%s", line)
		}
	}
	save := func() {
		rep.EndedAt = cfg.now().Format(time.RFC3339)
		rep.DurationMs = cfg.now().Sub(start).Milliseconds()
		if err := SaveRun(outDir, rep); err != nil {
			logf("写报告失败：%v", err)
		}
		_ = os.WriteFile(filepath.Join(outDir, "run.log"), []byte(logBuf.String()), 0o644)
	}
	fail := func(stop string, err error) (*RunReport, error) {
		rep.Stop = stop
		rep.StopDetail = err.Error()
		logf("停止：%s（%v）", stop, err)
		save()
		return rep, err
	}

	if !isGitRepo(ctx, repo) {
		return fail("preflight-error", fmt.Errorf("%s 不是 git 仓库", repo))
	}
	if !cfg.AllowDirty {
		st, err := statusPorcelain(ctx, repo)
		if err != nil {
			return fail("preflight-error", err)
		}
		if strings.TrimSpace(st) != "" {
			return fail("preflight-error", fmt.Errorf("主工作区有未提交改动，先收口再开自迭代：\n%s", clip(st, 800)))
		}
	}
	if err := ensureBranch(ctx, repo, cfg.TargetBranch, cfg.BaseBranch); err != nil {
		return fail("preflight-error", err)
	}
	targetDir, err := prepareWorktree(ctx, repo, workRoot, "target", cfg.TargetBranch, false, "")
	if err != nil {
		return fail("preflight-error", err)
	}
	keepTarget := false
	defer func() {
		if !keepTarget {
			removeWorktree(ctx, repo, targetDir)
		}
	}()

	logf("=== evolve %s ===", runID)
	logf("repo=%s", repo)
	logf("base=%s target=%s", cfg.BaseBranch, cfg.TargetBranch)
	if err := updateTarget(ctx, targetDir, cfg.BaseBranch); err != nil {
		return fail("preflight-error", err)
	}

	var baseEval eval.Report
	if cfg.Eval != nil {
		baseEval, err = cfg.Eval(ctx, targetDir)
		if err != nil {
			if !cfg.DryRun {
				return fail("preflight-error", fmt.Errorf("基线评测失败：%w", err))
			}
			logf("基线评测失败（dry-run 忽略）：%v", err)
		} else {
			rep.BaseEval = baseEval
			logf("基线评测：通过 %d / 失败 %d / 跳过 %d", baseEval.Pass, baseEval.Fail, baseEval.Skip)
		}
	}

	signals := CollectSignals(ctx, targetDir)
	history := LoadHistory(repo, 20)
	if len(signals) == 0 {
		signals = append(signals, "没有显式信号；请从代码质量、测试覆盖或已知小问题里找低风险改进。")
	}

	propose := func() (ProposeResult, error) {
		return cfg.Propose(ctx, ProposeInput{
			Repo:    repo,
			BaseDir: targetDir,
			Goal:    cfg.Goal,
			Signals: signals,
			History: history,
		})
	}
	tokens := 0

	if cfg.DryRun {
		pr, err := propose()
		if err != nil {
			return fail("propose-error", err)
		}
		tokens += pr.Tokens
		rep.Tokens = tokens
		rep.Proposals = pr.Proposals
		rep.Stop = "dry-run"
		logf("dry-run：收到 %d 个提案，不执行", len(pr.Proposals))
		for i, p := range pr.Proposals {
			logf("  [%d] [%s] %s", i+1, p.Risk, p.Title)
		}
		save()
		return rep, nil
	}

	consecutiveFailures := 0
	seen := map[string]bool{}
	stop := ""
	stopDetail := ""
	record := func(exp Experiment) {
		rep.Experiments = append(rep.Experiments, exp)
		if err := AppendHistory(repo, exp); err != nil {
			logf("写实验历史失败：%v", err)
		}
		save()
	}
	failed := func(exp *Experiment, status, reason string) {
		exp.Status = status
		exp.Reason = reason
		exp.EndedAt = cfg.now().Format(time.RFC3339)
		consecutiveFailures++
		logf("[%s] %s：%s", exp.ID, status, clip(reason, 300))
	}

	for i := 0; i < cfg.MaxCandidates; i++ {
		if ctx.Err() != nil {
			stop, stopDetail = "cancelled", ctx.Err().Error()
			break
		}
		if cfg.MaxWallClock > 0 && cfg.now().Sub(start) >= cfg.MaxWallClock {
			stop, stopDetail = "max-wall-clock", "达到本轮时间上限"
			break
		}
		if cfg.MaxTokens > 0 && tokens >= cfg.MaxTokens {
			stop, stopDetail = "max-tokens", "达到本轮 token 上限"
			break
		}

		pr, err := propose()
		if err != nil {
			stop, stopDetail = "propose-error", err.Error()
			break
		}
		tokens += pr.Tokens
		proposals := pr.Proposals
		if len(proposals) == 0 {
			stop, stopDetail = "no-proposals", "提案器没有给出可执行提案"
			break
		}
		rep.Proposals = append(rep.Proposals, proposals...)
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
			ID:        fmt.Sprintf("%s-c%d", runID, i+1),
			Title:     p.Title,
			Risk:      p.Risk,
			Status:    StatusProposed,
			Proposal:  p,
			StartedAt: cfg.now().Format(time.RFC3339),
		}
		logf("[%s] 提案 [%s] %s", exp.ID, p.Risk, p.Title)

		if p.Risk == RiskC {
			exp.Status = StatusSkipped
			exp.Reason = "风险 C：不自动合并，留给人工"
			exp.EndedAt = cfg.now().Format(time.RFC3339)
			logf("[%s] 跳过（风险 C）", exp.ID)
			record(exp)
			consecutiveFailures++
			if consecutiveFailures >= 2 {
				stop, stopDetail = "stalled", "连续两次提案被跳过/失败"
				break
			}
			continue
		}

		candBranch := "rsi/cand-" + exp.ID
		candDir, err := prepareWorktree(ctx, repo, workRoot, "cand-"+exp.ID, candBranch, true, cfg.TargetBranch)
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
			removeWorktree(ctx, repo, candDir)
			deleteBranch(ctx, repo, candBranch)
		}

		t0 := cfg.now()
		impl, err := cfg.Implement(ctx, ImplementInput{
			Repo:      repo,
			BaseDir:   targetDir,
			WorkDir:   candDir,
			Proposal:  p,
			MaxRounds: cfg.MaxRounds,
		})
		exp.Tokens = impl.Tokens
		exp.Summary = impl.Summary
		tokens += impl.Tokens
		exp.DurationMs = cfg.now().Sub(t0).Milliseconds()
		if err != nil {
			if p := captureWorktreePatch(ctx, candDir); p != "" {
				_ = WritePatch(outDir, exp.ID, p)
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

		commit, err := commitAll(ctx, candDir, "rsi: "+p.Title)
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

		if d, err := diffStat(ctx, repo, cfg.TargetBranch, candBranch); err == nil {
			exp.Diff = d
			logf("[%s] 改动：%d 个文件 +%d/-%d", exp.ID, d.Files, d.Added, d.Deleted)
		}
		if patch, err := gitOut(ctx, repo, "diff", cfg.TargetBranch+"..."+candBranch); err == nil {
			_ = WritePatch(outDir, exp.ID, patch)
		}

		gate, err := cfg.Gate(ctx, GateInput{
			BaseDir:    targetDir,
			CandDir:    candDir,
			BaseBranch: cfg.TargetBranch,
			CandBranch: candBranch,
			BaseEval:   baseEval,
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
		logf("[%s] 门禁通过", exp.ID)

		merged, err := mergeCandidate(ctx, targetDir, candBranch, "rsi: merge "+exp.ID+" "+p.Title)
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
		if candTree, e1 := gitOut(ctx, repo, "rev-parse", exp.Commit+"^{tree}"); e1 == nil {
			if targetTree, e2 := gitOut(ctx, targetDir, "rev-parse", "HEAD^{tree}"); e2 == nil && candTree == targetTree {
				exp.Status = StatusMerged
				exp.Reason = "门禁通过，合并后树与候选一致（跳过重复门禁）"
				exp.EndedAt = cfg.now().Format(time.RFC3339)
				baseEval = gate.Eval
				consecutiveFailures = 0
				logf("[%s] 已合入 %s（%s，树一致）", exp.ID, cfg.TargetBranch, shortHash(merged))
				cleanupCand()
				record(exp)
				continue
			}
		}

		post, err := cfg.Gate(ctx, GateInput{
			BaseDir:    targetDir,
			CandDir:    targetDir,
			BaseBranch: cfg.TargetBranch,
			CandBranch: cfg.TargetBranch,
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
			if rev, rerr := revertLastMerge(ctx, targetDir); rerr != nil {
				keepTarget = true
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
		exp.Reason = "门禁通过，已合入 " + cfg.TargetBranch
		exp.EndedAt = cfg.now().Format(time.RFC3339)
		baseEval = gate.Eval
		consecutiveFailures = 0
		logf("[%s] 已合入 %s（%s）", exp.ID, cfg.TargetBranch, shortHash(merged))
		cleanupCand()
		record(exp)
	}

	if stop == "" {
		stop = "max-candidates"
		stopDetail = fmt.Sprintf("达到本轮候选上限 %d", cfg.MaxCandidates)
	}
	rep.Stop = stop
	rep.StopDetail = stopDetail
	rep.Tokens = tokens
	save()
	logf("=== 结束：%s（%s），候选 %d 个 ===", stop, stopDetail, len(rep.Experiments))
	return rep, nil
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
