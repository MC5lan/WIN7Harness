package core

import "testing"

// 规则前缀匹配必须带边界：git push 命中 git push origin main，
// 但不命中 git pushxxx；路径前缀同理。
func TestRuleMatchesPrefixBoundary(t *testing.T) {
	// 用写类工具 + ask 模式验证 allow 规则的边界（只读操作本就自动放行）
	p := NewPolicy(PermAsk, []string{"run_command:git push"}, nil, nil, nil)

	cases := []struct {
		target string
		want   string // allow（命中规则）或 ask（越界/未命中）
	}{
		{"git push", "allow"},
		{"git push origin main", "allow"},
		{"git pushxxx", "ask"},
		{"git pushupstream", "ask"},
	}
	for _, c := range cases {
		got := p.Decide("run_command", c.target, true, RiskSafe, "")
		if got.Kind != c.want {
			t.Fatalf("git push 对 %q 期望 %v，实际 %v", c.target, c.want, got.Kind)
		}
	}
}

// 问号按 rune 计数：中文一个字符算一位。
func TestWildMatchRuneQuestionMark(t *testing.T) {
	if !wildMatch("工具?读", "工具A读") {
		t.Fatal("? 应匹配一个中文字符")
	}
	if wildMatch("工具?读", "工具AA读") {
		t.Fatal("? 不应匹配两个字符")
	}
}

// 高危命令即使在 auto + allow 规则下，也必须 ask。
func TestDangerousAlwaysAsks(t *testing.T) {
	p := NewPolicy(PermAuto, []string{"run_command:*"}, nil, nil, nil)
	got := p.Decide("run_command", "rm -rf /tmp/x", false, RiskDanger, "")
	if got.Kind != "ask" {
		t.Fatalf("高危命令应 ask，实际 %v（auto + allow * 不该放行）", got.Kind)
	}
	if !p.Decide("run_command", "git status", false, RiskSafe, "").Allow() {
		t.Fatal("普通命令应放行")
	}
}

// deny 规则压过 allow。
func TestDenyBeatsAllow(t *testing.T) {
	p := NewPolicy(PermAuto, []string{"*:git *"}, []string{"run_command:git push"}, nil, nil)
	got := p.Decide("run_command", "git push origin main", false, RiskSafe, "")
	if got.Kind != "deny" {
		t.Fatalf("deny 应优先，实际 %v", got.Kind)
	}
}
