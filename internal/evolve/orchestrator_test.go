package evolve

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"win7harness/internal/eval"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v 失败：%v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitTest(t, dir, "init", "-b", "main")
	gitTest(t, dir, "config", "user.name", "test")
	gitTest(t, dir, "config", "user.email", "test@example.com")
	writeTestFile(t, filepath.Join(dir, "README.md"), "hello\n")
	writeTestFile(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.20\n")
	gitTest(t, dir, "add", "-A")
	gitTest(t, dir, "commit", "-m", "init")
	return dir
}

func passEval() eval.Report {
	return eval.Report{Pass: 1, Results: []eval.Result{{ID: "B01", Status: eval.StatusPass}}}
}

func TestRunShadowMerge(t *testing.T) {
	repo := newTestRepo(t)
	workRoot := t.TempDir()
	gateCalls := 0
	cfg := Config{
		RunID:         "t-shadow",
		Repo:          repo,
		WorkRoot:      workRoot,
		BaseBranch:    "main",
		TargetBranch:  "rsi/auto",
		MaxCandidates: 1,
		MaxDiffLines:  100,
		MaxFiles:      5,
		Propose: func(context.Context, ProposeInput) (ProposeResult, error) {
			return ProposeResult{Proposals: []Proposal{{Title: "加文件", Change: "写 feature.txt", Acceptance: "文件存在", Risk: RiskA}}}, nil
		},
		Implement: func(_ context.Context, in ImplementInput) (ImplementResult, error) {
			writeTestFile(t, filepath.Join(in.WorkDir, "feature.txt"), "ok\n")
			return ImplementResult{Summary: "写了 feature.txt", Tokens: 12}, nil
		},
		Gate: func(_ context.Context, in GateInput) (GateResult, error) {
			gateCalls++
			return GateResult{OK: true, BaseEval: in.BaseEval, Eval: passEval()}, nil
		},
		Eval: func(context.Context, string) (eval.Report, error) { return passEval(), nil },
		Logf: t.Logf,
	}
	rep, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Experiments) != 1 || rep.Experiments[0].Status != StatusMerged {
		t.Fatalf("候选应合入影子分支：%+v", rep.Experiments)
	}
	if gateCalls != 1 {
		t.Fatalf("目标分支未前进时应跳过重复的合并后门禁，实得 %d 次", gateCalls)
	}
	if got := gitTest(t, repo, "show", "rsi/auto:feature.txt"); got != "ok" {
		t.Fatalf("影子分支应包含改动，实得 %q", got)
	}
	if _, err := gitTestErr(repo, "show", "main:feature.txt"); err == nil {
		t.Fatal("main 不应被改动")
	}
	if _, err := os.Stat(filepath.Join(repo, "rsi-out", "t-shadow", "run.md")); err != nil {
		t.Fatalf("报告未落盘：%v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "rsi-out", "history.jsonl")); err != nil {
		t.Fatalf("实验历史未落盘：%v", err)
	}
}

func TestRunGateFailureDoesNotMerge(t *testing.T) {
	repo := newTestRepo(t)
	cfg := Config{
		RunID:         "t-fail",
		Repo:          repo,
		WorkRoot:      t.TempDir(),
		BaseBranch:    "main",
		TargetBranch:  "rsi/auto",
		MaxCandidates: 1,
		MaxDiffLines:  100,
		MaxFiles:      5,
		Propose: func(context.Context, ProposeInput) (ProposeResult, error) {
			return ProposeResult{Proposals: []Proposal{{Title: "坏改动", Change: "写 bad.txt", Acceptance: "门禁通过", Risk: RiskB}}}, nil
		},
		Implement: func(_ context.Context, in ImplementInput) (ImplementResult, error) {
			writeTestFile(t, filepath.Join(in.WorkDir, "bad.txt"), "bad\n")
			return ImplementResult{Summary: "写了 bad.txt"}, nil
		},
		Gate: func(context.Context, GateInput) (GateResult, error) {
			return GateResult{OK: false, Failures: []string{"测试红"}}, nil
		},
		Eval: func(context.Context, string) (eval.Report, error) { return passEval(), nil },
		Logf: t.Logf,
	}
	rep, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Experiments) != 1 || rep.Experiments[0].Status != StatusGateFailed {
		t.Fatalf("候选应被门禁拦住：%+v", rep.Experiments)
	}
	if _, err := gitTestErr(repo, "show", "rsi/auto:bad.txt"); err == nil {
		t.Fatal("门禁失败不应合入影子分支")
	}
	if _, err := gitTestErr(repo, "show", "main:bad.txt"); err == nil {
		t.Fatal("门禁失败不应改动 main")
	}
}

func TestRunDryRun(t *testing.T) {
	repo := newTestRepo(t)
	cfg := Config{
		RunID:        "t-dry",
		Repo:         repo,
		WorkRoot:     t.TempDir(),
		BaseBranch:   "main",
		TargetBranch: "rsi/auto",
		DryRun:       true,
		Propose: func(context.Context, ProposeInput) (ProposeResult, error) {
			return ProposeResult{Proposals: []Proposal{{Title: "只提案", Change: "不改", Acceptance: "n/a", Risk: RiskA}}}, nil
		},
		Eval: func(context.Context, string) (eval.Report, error) { return passEval(), nil },
		Logf: t.Logf,
	}
	rep, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Stop != "dry-run" || len(rep.Proposals) != 1 {
		t.Fatalf("dry-run 结果不对：stop=%q proposals=%d", rep.Stop, len(rep.Proposals))
	}
	if len(rep.Experiments) != 0 {
		t.Fatal("dry-run 不应执行候选")
	}
}

func TestRunStopsOnDuplicateProposal(t *testing.T) {
	repo := newTestRepo(t)
	calls := 0
	cfg := Config{
		RunID:         "t-dup",
		Repo:          repo,
		WorkRoot:      t.TempDir(),
		BaseBranch:    "main",
		TargetBranch:  "rsi/auto",
		MaxCandidates: 2,
		MaxDiffLines:  100,
		MaxFiles:      5,
		Propose: func(context.Context, ProposeInput) (ProposeResult, error) {
			calls++
			return ProposeResult{Proposals: []Proposal{{Title: "同一个提案", Change: "写 f.txt", Acceptance: "存在", Risk: RiskA}}}, nil
		},
		Implement: func(_ context.Context, in ImplementInput) (ImplementResult, error) {
			writeTestFile(t, filepath.Join(in.WorkDir, "f.txt"), "x\n")
			return ImplementResult{Summary: "ok"}, nil
		},
		Gate: func(_ context.Context, in GateInput) (GateResult, error) {
			return GateResult{OK: true, BaseEval: in.BaseEval, Eval: passEval()}, nil
		},
		Eval: func(context.Context, string) (eval.Report, error) { return passEval(), nil },
		Logf: t.Logf,
	}
	rep, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("应提案两次（第二次发现重复），实得 %d", calls)
	}
	if len(rep.Experiments) != 1 || rep.Stop != "no-new-proposals" {
		t.Fatalf("重复提案应停止且只执行一次：stop=%q exp=%d", rep.Stop, len(rep.Experiments))
	}
}

func TestRunRejectsDirtyRepo(t *testing.T) {
	repo := newTestRepo(t)
	writeTestFile(t, filepath.Join(repo, "dirty.txt"), "x")
	cfg := Config{
		Repo:         repo,
		WorkRoot:     t.TempDir(),
		BaseBranch:   "main",
		TargetBranch: "rsi/auto",
		Propose: func(context.Context, ProposeInput) (ProposeResult, error) {
			return ProposeResult{}, nil
		},
		Implement: func(context.Context, ImplementInput) (ImplementResult, error) {
			return ImplementResult{}, nil
		},
		Gate: func(context.Context, GateInput) (GateResult, error) {
			return GateResult{OK: true}, nil
		},
		Logf: t.Logf,
	}
	if _, err := Run(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "未提交改动") {
		t.Fatalf("脏工作区应被拒绝，实得 %v", err)
	}
}

func TestHasChangesIgnoresGeneratedAgents(t *testing.T) {
	repo := newTestRepo(t)
	if ok, err := HasChanges(context.Background(), repo); err != nil || ok {
		t.Fatalf("干净仓库不应有改动：ok=%v err=%v", ok, err)
	}
	writeTestFile(t, filepath.Join(repo, "AGENTS.md"), "auto\n")
	if ok, err := HasChanges(context.Background(), repo); err != nil || ok {
		t.Fatalf("只有自动 AGENTS.md 不算改动：ok=%v err=%v", ok, err)
	}
	writeTestFile(t, filepath.Join(repo, "f.txt"), "x\n")
	if ok, err := HasChanges(context.Background(), repo); err != nil || !ok {
		t.Fatalf("真实文件应算改动：ok=%v err=%v", ok, err)
	}
}

func TestCommitAllIgnoresGeneratedAgents(t *testing.T) {
	repo := newTestRepo(t)
	writeTestFile(t, filepath.Join(repo, "AGENTS.md"), "auto\n")
	if _, err := commitAll(context.Background(), repo, "noop"); err != errNoChanges {
		t.Fatalf("只有自动 AGENTS.md 时应判无改动，实得 %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("未跟踪的自动 AGENTS.md 应被清理")
	}
	writeTestFile(t, filepath.Join(repo, "f.txt"), "x\n")
	writeTestFile(t, filepath.Join(repo, "AGENTS.md"), "auto\n")
	commit, err := commitAll(context.Background(), repo, "real")
	if err != nil {
		t.Fatal(err)
	}
	if got := gitTest(t, repo, "show", commit+":f.txt"); got != "x" {
		t.Fatalf("真实改动未提交：%q", got)
	}
	if _, err := gitTestErr(repo, "show", commit+":AGENTS.md"); err == nil {
		t.Fatal("自动 AGENTS.md 不应进入候选提交")
	}
}

func TestRevertLastMerge(t *testing.T) {
	repo := newTestRepo(t)
	gitTest(t, repo, "checkout", "-b", "cand")
	writeTestFile(t, filepath.Join(repo, "f.txt"), "x\n")
	gitTest(t, repo, "add", "-A")
	gitTest(t, repo, "commit", "-m", "cand")
	gitTest(t, repo, "checkout", "main")
	gitTest(t, repo, "merge", "--no-ff", "--no-edit", "cand")
	if _, err := gitTestErr(repo, "show", "HEAD:f.txt"); err != nil {
		t.Fatal("合并后应存在 f.txt")
	}
	if _, err := revertLastMerge(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	if _, err := gitTestErr(repo, "show", "HEAD:f.txt"); err == nil {
		t.Fatal("回滚后 f.txt 应消失")
	}
}

// gitTestErr 与 gitTest 相反：只关心失败，用来断言某个路径不存在。
func gitTestErr(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
