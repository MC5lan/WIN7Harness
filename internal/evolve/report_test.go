package evolve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveRunAndHistory(t *testing.T) {
	repo := t.TempDir()
	rep := &RunReport{
		ID:           "t1",
		StartedAt:    "2026-01-01T00:00:00Z",
		EndedAt:      "2026-01-01T00:01:00Z",
		Repo:         repo,
		BaseBranch:   "master",
		TargetBranch: "rsi/auto",
		Stop:         "max-candidates",
		Experiments: []Experiment{{
			ID: "t1-c1", Title: "加测试", Risk: RiskA, Status: StatusMerged,
			Gate: &GateResult{OK: true, Steps: []GateStep{{Name: "go test", OK: true}}},
		}},
	}
	out := filepath.Join(repo, "rsi-out", rep.ID)
	if err := SaveRun(out, rep); err != nil {
		t.Fatal(err)
	}
	md, err := os.ReadFile(filepath.Join(out, "run.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"自迭代报告", "t1-c1", "go test", "影子阶段"} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("报告缺少 %q：\n%s", want, md)
		}
	}
	for _, id := range []string{"e0", "e1", "e2"} {
		if err := AppendHistory(repo, Experiment{ID: id, Title: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	got := LoadHistory(repo, 2)
	if len(got) != 2 || got[1].ID != "e2" {
		t.Fatalf("历史应取最近 2 条：%+v", got)
	}
}

func TestMarkdownDryRun(t *testing.T) {
	rep := &RunReport{
		ID: "d1", StartedAt: time.Now().Format(time.RFC3339), DryRun: true,
		BaseBranch: "master", TargetBranch: "rsi/auto", Stop: "dry-run",
		Proposals: []Proposal{{Title: "只提案", Risk: RiskB, Change: "不改", Acceptance: "n/a"}},
	}
	md := rep.Markdown()
	if !strings.Contains(md, "dry-run") || !strings.Contains(md, "只提案") {
		t.Fatalf("dry-run 报告不对：\n%s", md)
	}
}
