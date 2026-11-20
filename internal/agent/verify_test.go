package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"win7harness/internal/llm"
)

func TestVerifyEnabledSwitch(t *testing.T) {
	t.Setenv("W7H_VERIFY", "")
	e := &Engine{}
	if e.verifyEnabled() {
		t.Fatal("默认应关闭自动验证")
	}
	e.VerifyMode = VerifyModeAuto
	if !e.verifyEnabled() {
		t.Fatal("auto 模式应启用")
	}
	e.VerifyMode = VerifyModeOff
	if e.verifyEnabled() {
		t.Fatal("off 模式应关闭")
	}
	e.VerifyMode = ""
	t.Setenv("W7H_VERIFY", "1")
	if !e.verifyEnabled() {
		t.Fatal("环境变量 W7H_VERIFY=1 应启用")
	}
}

func TestMarkWriteOnlyForWriteTools(t *testing.T) {
	e := &Engine{}
	e.resetWriteFlag()
	// 只读工具不该触发验证
	e.MarkWrite("read_file")
	e.MarkWrite("list_dir")
	if e.wroteThisTurnNow() {
		t.Fatal("只读工具不应置位")
	}
	e.MarkWrite("write_file")
	if !e.wroteThisTurnNow() {
		t.Fatal("写类工具应置位")
	}
	e.resetWriteFlag()
	if e.wroteThisTurnNow() {
		t.Fatal("重置后应清零")
	}
}

// 没改过代码时不跑验证：纯问答不该白等一次构建。
func TestAutoVerifySkippedWithoutWrite(t *testing.T) {
	e := &Engine{VerifyMode: VerifyModeAuto}
	tries := 0
	if _, ran := e.autoVerify(context.Background(), &tries); ran {
		t.Fatal("未写盘不应跑验证")
	}
	if tries != 0 {
		t.Fatal("未跑验证不应消耗重试次数")
	}
}

// 端到端：把工作区指到一个真 go module，改过代码后应自动验证通过。
func TestAutoVerifyPass(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实构建")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n\ngo 1.20\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package probe\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a_test.go"),
		[]byte("package probe\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1,2) != 3 { t.Fatal(\"bad\") } }\n"), 0o644)

	var notices []string
	e := &Engine{
		WorkDir:         dir,
		VerifyMode:      VerifyModeAuto,
		VerifyTimeout:   120 * time.Second,
		MaxVerifyRounds: 2,
		Emit: func(ev Event) {
			if ev.Kind == "notice" {
				notices = append(notices, ev.Text)
			}
		},
	}
	e.MarkWrite("write_file")
	tries := 0
	report, ran := e.autoVerify(context.Background(), &tries)
	if !ran {
		t.Fatal("应执行验证")
	}
	if report != "" {
		t.Fatalf("验证通过不应回灌失败报告：%s", report)
	}
	if tries != 1 {
		t.Fatalf("重试计数 = %d，期望 1", tries)
	}
	if len(notices) == 0 || !strings.Contains(notices[0], "验证通过") {
		t.Fatalf("应提示验证通过，实际 %v", notices)
	}
}

// 端到端：编译不过时回灌报告，并且报告里有可定位的错误信息。
func TestAutoVerifyFailFeedsBack(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实构建")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n\ngo 1.20\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package probe\n\nfunc broken() { undefinedSymbol() }\n"), 0o644)

	e := &Engine{
		WorkDir:         dir,
		VerifyMode:      VerifyModeAuto,
		VerifyTimeout:   120 * time.Second,
		MaxVerifyRounds: 3,
	}
	e.MarkWrite("edit_file")
	tries := 0
	report, ran := e.autoVerify(context.Background(), &tries)
	if !ran {
		t.Fatal("应执行验证")
	}
	if report == "" {
		t.Fatal("验证失败应回灌报告")
	}
	if !strings.Contains(report, "自动验证") || !strings.Contains(report, "a.go") {
		t.Fatalf("回灌报告缺少定位信息：%s", report)
	}
	if !strings.Contains(report, "不要忽略失败") {
		t.Fatal("回灌报告缺少收尾约束说明")
	}
}

// 达到上限后不再跑，避免和环境问题死磕。
func TestAutoVerifyRoundLimit(t *testing.T) {
	e := &Engine{VerifyMode: VerifyModeAuto, MaxVerifyRounds: 2, WorkDir: t.TempDir()}
	e.MarkWrite("write_file")
	tries := 2
	var notices []string
	e.Emit = func(ev Event) {
		if ev.Kind == "notice" {
			notices = append(notices, ev.Text)
		}
	}
	report, ran := e.autoVerify(context.Background(), &tries)
	if ran || report != "" {
		t.Fatal("超过上限后不应再验证")
	}
	if len(notices) == 0 || !strings.Contains(notices[0], "上限") {
		t.Fatalf("应提示已达上限，实际 %v", notices)
	}
}

func TestCacheStats(t *testing.T) {
	e := &Engine{Model: "deepseek-chat"}
	e.addUsage(llm.Usage{PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100, CachedTokens: 600})
	e.addUsage(llm.Usage{PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100, CachedTokens: 400})
	s := e.CostSnapshot()
	if s.TokIn != 2000 || s.Cached != 1000 || s.Turns != 2 {
		t.Fatalf("累计值异常：%+v", s)
	}
	if s.HitRate < 0.49 || s.HitRate > 0.51 {
		t.Fatalf("命中率 = %.2f，期望 0.5", s.HitRate)
	}
	if s.Cost >= s.CostPlain {
		t.Fatalf("有缓存时费用应更低：%v vs %v", s.Cost, s.CostPlain)
	}
	if s.LastHit < 0.39 || s.LastHit > 0.41 {
		t.Fatalf("最近一轮命中率 = %.2f，期望 0.4", s.LastHit)
	}
	board := e.CostBoard()
	for _, want := range []string{"成本看板", "命中率", "估算费用", "无缓存需"} {
		if !strings.Contains(board, want) {
			t.Fatalf("看板缺少 %q：\n%s", want, board)
		}
	}
	if line := e.CacheLine(); !strings.Contains(line, "缓存命中") {
		t.Fatalf("缓存行格式异常：%s", line)
	}
}

// 没有缓存命中时要给出可操作的解释，而不是只显示 0。
func TestCacheLineZeroHitHint(t *testing.T) {
	e := &Engine{Model: "gpt-4o"}
	e.addUsage(llm.Usage{PromptTokens: 500, CompletionTokens: 50})
	line := e.CacheLine()
	if !strings.Contains(line, "命中率为 0") {
		t.Fatalf("应给出 0 命中的解释：%s", line)
	}
}
