package core

import "testing"

func TestParseRunArgsBasic(t *testing.T) {
	opt, ok := ParseRunArgs([]string{"-run", "把 TODO 汇总写到 todo.md"})
	if !ok || opt.Prompt != "把 TODO 汇总写到 todo.md" {
		t.Fatalf("基本形式解析失败: %+v ok=%v", opt, ok)
	}
	if opt.Yes || opt.Quiet || opt.Format != "text" {
		t.Fatalf("默认值不对: %+v", opt)
	}
}

func TestParseRunArgsEqualsForm(t *testing.T) {
	opt, ok := ParseRunArgs([]string{"--run=整理 src 目录", "--workdir", `D:\proj`, "--yes", "--json"})
	if !ok || opt.Prompt != "整理 src 目录" || opt.WorkDir != `D:\proj` || !opt.Yes || opt.Format != "json" {
		t.Fatalf("等号形式解析失败: %+v", opt)
	}
}

func TestParseRunArgsStdinMarker(t *testing.T) {
	opt, ok := ParseRunArgs([]string{"-run", "-"})
	if !ok || opt.Prompt != "-" {
		t.Fatalf("标准输入标记应被接受: %+v ok=%v", opt, ok)
	}
}

func TestParseRunArgsNoRunFlag(t *testing.T) {
	if _, ok := ParseRunArgs([]string{"--debug"}); ok {
		t.Fatal("没有 -run 时不应判定为命令行模式")
	}
	if _, ok := ParseRunArgs(nil); ok {
		t.Fatal("空参数不应判定为命令行模式")
	}
}

func TestParseRunArgsTimeoutAndPlan(t *testing.T) {
	opt, _ := ParseRunArgs([]string{"-run", "x", "--timeout", "90", "--plan", "-q"})
	if opt.Timeout != 90 || !opt.Plan || !opt.Quiet {
		t.Fatalf("超时/计划模式解析失败: %+v", opt)
	}
	opt, _ = ParseRunArgs([]string{"-run", "x", "--timeout", "abc"})
	if opt.Timeout != 0 {
		t.Fatalf("非法超时应归零: %+v", opt)
	}
}

func TestParseEvalFlag(t *testing.T) {
	for _, form := range [][]string{{"-eval"}, {"--eval"}, {"/eval"}, {"-eval", "--json"}} {
		opt, ok := ParseRunArgs(form)
		if !ok || !opt.Eval {
			t.Fatalf("%v 未识别为评测模式: %+v ok=%v", form, opt, ok)
		}
		if msg := opt.Validate(); msg != "" {
			t.Fatalf("%v 校验应通过，实得：%s", form, msg)
		}
	}
}

func TestEvalFlagConflicts(t *testing.T) {
	opt, _ := ParseRunArgs([]string{"-eval", "-run", "干活"})
	if msg := opt.Validate(); msg == "" {
		t.Fatal("-eval 与 -run 应互斥")
	}
	opt2, _ := ParseRunArgs([]string{"-eval", "-selftest"})
	if msg := opt2.Validate(); msg == "" {
		t.Fatal("-eval 与 -selftest 应互斥")
	}
}
