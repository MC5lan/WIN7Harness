package evolve

import (
	"strings"
	"testing"
)

func TestParseProposalsFencedJSON(t *testing.T) {
	raw := "```json\n" + `[
		{"title":"补测试","hypothesis":"覆盖快照去重","files":["internal/core/snapshot_test.go"],"change":"加一个用例","acceptance":"go test ./internal/core 通过","risk":"a"},
		{"title":"坏提案","change":"","acceptance":""}
	]` + "\n```"
	got, err := ParseProposals(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("应只保留 1 条合法提案，实得 %d", len(got))
	}
	if got[0].Risk != RiskA {
		t.Fatalf("风险应归一化为 A，实得 %q", got[0].Risk)
	}
	if got[0].Title != "补测试" {
		t.Fatalf("标题不对：%q", got[0].Title)
	}
}

func TestParseProposalsDefaultsRiskC(t *testing.T) {
	got, err := ParseProposals(`[{"title":"x","change":"y","acceptance":"z"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Risk != RiskC {
		t.Fatalf("缺省风险必须是 C（最保守），实得 %q", got[0].Risk)
	}
}

func TestParseProposalsEmptyArray(t *testing.T) {
	got, err := ParseProposals(`[]`)
	if err != nil {
		t.Fatalf("空数组不应报错：%v", err)
	}
	if len(got) != 0 {
		t.Fatalf("空数组应返回 0 条，实得 %d", len(got))
	}
}

func TestValidateProposalRejectsUnsafePath(t *testing.T) {
	p := NormalizeProposal(Proposal{Title: "x", Change: "y", Acceptance: "z", Files: []string{"../outside"}})
	if err := ValidateProposal(p); err == nil {
		t.Fatal("应拒绝越界路径")
	}
}

func TestPromptsContainGuardrails(t *testing.T) {
	pp := BuildProposePrompt(ProposeInput{
		Signals: []string{"PROGRESS 待办：补测试"},
		History: []Experiment{{Title: "上次失败", Status: StatusGateFailed, Risk: RiskB, Reason: "测试红"}},
	})
	for _, want := range []string{"internal/eval", "JSON", "补测试", "上次失败"} {
		if !strings.Contains(pp, want) {
			t.Fatalf("提案提示词缺少 %q", want)
		}
	}
	ip := BuildImplementPrompt(ImplementInput{
		WorkDir:  `D:\tmp\cand`,
		Proposal: Proposal{Title: "补测试", Change: "加用例", Acceptance: "通过", Risk: RiskA},
	})
	for _, want := range []string{"隔离", "go build", "不要执行 git 写操作", "补测试"} {
		if !strings.Contains(ip, want) {
			t.Fatalf("实现提示词缺少 %q", want)
		}
	}
}
