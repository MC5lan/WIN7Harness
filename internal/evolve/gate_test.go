package evolve

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"win7harness/internal/eval"
)

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckProtected(t *testing.T) {
	base := t.TempDir()
	cand := t.TempDir()
	for _, dir := range []string{base, cand} {
		writeTestFile(t, filepath.Join(dir, "internal", "eval", "eval.go"), "package eval\n")
		writeTestFile(t, filepath.Join(dir, "code.go"), "package demo\n")
	}
	if _, err := checkProtected(context.Background(), base, cand, []string{"internal/eval"}); err != nil {
		t.Fatalf("完全一致时不应报错：%v", err)
	}
	writeTestFile(t, filepath.Join(cand, "internal", "eval", "eval.go"), "package eval\n// 改了尺子\n")
	_, err := checkProtected(context.Background(), base, cand, []string{"internal/eval"})
	if err == nil || !strings.Contains(err.Error(), "internal/eval/eval.go") {
		t.Fatalf("应报出被改的受保护文件，实得 %v", err)
	}
}

func TestCheckProtectedAddedFile(t *testing.T) {
	base := t.TempDir()
	cand := t.TempDir()
	writeTestFile(t, filepath.Join(cand, "internal", "eval", "extra.go"), "package eval\n")
	_, err := checkProtected(context.Background(), base, cand, []string{"internal/eval"})
	if err == nil || !strings.Contains(err.Error(), "internal/eval/extra.go") {
		t.Fatalf("新增受保护文件也应被拦住，实得 %v", err)
	}
}

func TestDefaultProtectedIncludesGateEntrypoints(t *testing.T) {
	want := map[string]bool{
		"cmd/evalrun": false, "cmd/selftest_run": false,
		"cmd/bench": false, "internal/bench": false,
	}
	for _, p := range DefaultProtected {
		if _, ok := want[p]; ok {
			want[p] = true
		}
	}
	for p, ok := range want {
		if !ok {
			t.Fatalf("DefaultProtected 缺少门禁入口 %s", p)
		}
	}
}

func TestCmpDetail(t *testing.T) {
	got := cmpDetail(eval.Comparison{Regressions: []string{"B01"}, Missing: []string{"B09"}})
	if got != "退步：B01；消失：B09" {
		t.Fatalf("退步描述不对：%q", got)
	}
}

func TestCheckTestChangesRejectsDeletion(t *testing.T) {
	repo := newTestRepo(t)
	writeTestFile(t, filepath.Join(repo, "a_test.go"), "package demo\n\nfunc TestA(t *testing.T) {}\n\nfunc TestB(t *testing.T) {}\n")
	gitTest(t, repo, "add", "-A")
	gitTest(t, repo, "commit", "-m", "tests")
	gitTest(t, repo, "checkout", "-b", "cand")
	writeTestFile(t, filepath.Join(repo, "a_test.go"), "package demo\n\nfunc TestA(t *testing.T) {}\n")
	gitTest(t, repo, "add", "-A")
	gitTest(t, repo, "commit", "-m", "drop test")
	_, err := checkTestChanges(context.Background(), repo, "main", "cand")
	if err == nil || !strings.Contains(err.Error(), "a_test.go") {
		t.Fatalf("删测试应被拦住，实得 %v", err)
	}
}

func TestCheckTestChangesAllowsNewLines(t *testing.T) {
	repo := newTestRepo(t)
	writeTestFile(t, filepath.Join(repo, "a_test.go"), "package demo\n\nfunc TestA(t *testing.T) {}\n")
	gitTest(t, repo, "add", "-A")
	gitTest(t, repo, "commit", "-m", "tests")
	gitTest(t, repo, "checkout", "-b", "cand")
	writeTestFile(t, filepath.Join(repo, "a_test.go"), "package demo\n\nfunc TestA(t *testing.T) {}\n\nfunc TestB(t *testing.T) {}\n")
	gitTest(t, repo, "add", "-A")
	gitTest(t, repo, "commit", "-m", "add test")
	if _, err := checkTestChanges(context.Background(), repo, "main", "cand"); err != nil {
		t.Fatalf("只加测试行应允许：%v", err)
	}
}

func TestShouldBench(t *testing.T) {
	yes := []string{
		"internal/agent/engine.go",
		"internal/tools/fs.go",
		"internal/llm/client.go",
		"internal/skills/skills.go",
		"internal/bench/bench.go",
		"cmd/bench/main.go",
	}
	for _, f := range yes {
		if !shouldBench([]string{f}) {
			t.Fatalf("%s 应触发 bench", f)
		}
	}
	no := []string{
		"docs/rsi-v1.32.md",
		"internal/eval/eval.go",
		"internal/core/cliargs.go",
		"internal/app/app.go",
	}
	for _, f := range no {
		if shouldBench([]string{f}) {
			t.Fatalf("%s 不应触发 bench", f)
		}
	}
}

func TestDiffStat(t *testing.T) {
	repo := newTestRepo(t)
	gitTest(t, repo, "checkout", "-b", "cand")
	writeTestFile(t, filepath.Join(repo, "a.go"), "package demo\n\nfunc A() {}\n")
	writeTestFile(t, filepath.Join(repo, "b.txt"), "x\ny\n")
	gitTest(t, repo, "add", "-A")
	gitTest(t, repo, "commit", "-m", "cand")
	st, err := diffStat(context.Background(), repo, "main", "cand")
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 2 || st.Added < 3 {
		t.Fatalf("diff 统计不对：%+v", st)
	}
	files, err := changedFiles(context.Background(), repo, "main", "cand")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0] != "a.go" || files[1] != "b.txt" {
		t.Fatalf("变更文件不对：%v", files)
	}
}
