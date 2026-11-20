package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const goFailOut = `=== RUN   TestAdd
--- FAIL: TestAdd (0.00s)
    calc_test.go:42: got 3 want 4
=== RUN   TestSub
--- PASS: TestSub (0.00s)
--- FAIL: TestDiv (0.00s)
    calc_test.go:58: divide by zero not handled
FAIL
FAIL	example.com/calc	0.012s
ok  	example.com/util	0.004s
`

func TestParseGoFailures(t *testing.T) {
	s := parseTestOutput("go", goFailOut, 1)
	if s.Failed != 2 {
		t.Fatalf("期望 2 个失败，实得 %d", s.Failed)
	}
	if len(s.Failures) != 2 || s.Failures[0].Name != "TestAdd" {
		t.Fatalf("失败用例解析异常: %+v", s.Failures)
	}
	if !strings.Contains(s.Failures[0].Detail, "calc_test.go:42") {
		t.Fatalf("应带出文件行号，实得 %q", s.Failures[0].Detail)
	}
	if s.Passed != 1 {
		t.Fatalf("通过包数应为 1，实得 %d", s.Passed)
	}
	if s.Broken {
		t.Fatal("普通失败不应标记为未能运行")
	}
}

func TestParseGoBuildBroken(t *testing.T) {
	out := "# example.com/x\n./main.go:7:2: undefined: y\nFAIL\texample.com/x [build failed]\n"
	s := parseTestOutput("go", out, 1)
	if !s.Broken {
		t.Fatal("编译失败应标记 Broken")
	}
	if !strings.Contains(strings.Join(s.Notes, " "), "package") && !strings.Contains(strings.Join(s.Notes, " "), "编译") {
		t.Fatalf("应说明编译失败: %v", s.Notes)
	}
}

func TestParsePytest(t *testing.T) {
	out := `FAILED tests/test_a.py::test_login - AssertionError: 401 != 200
FAILED tests/test_a.py::test_logout - KeyError: 'token'
========================= 2 failed, 7 passed, 1 skipped in 0.8s =========================
`
	s := parseTestOutput("pytest", out, 1)
	if s.Failed != 2 || s.Passed != 7 || s.Skipped != 1 {
		t.Fatalf("统计不对: %+v", s)
	}
	if s.Failures[0].Name != "tests/test_a.py::test_login" {
		t.Fatalf("用例名不对: %q", s.Failures[0].Name)
	}
	if !strings.Contains(s.Failures[0].Detail, "401") {
		t.Fatalf("断言信息丢失: %q", s.Failures[0].Detail)
	}
}

func TestParseJest(t *testing.T) {
	out := `  ✕ adds numbers (5 ms)
  ✓ subtracts (1 ms)
Tests:       1 failed, 1 passed, 1 total
`
	s := parseTestOutput("npm", out, 1)
	if s.Failed != 1 || s.Passed != 1 {
		t.Fatalf("统计不对: %+v", s)
	}
	if len(s.Failures) != 1 || !strings.Contains(s.Failures[0].Name, "adds numbers") {
		t.Fatalf("用例名不对: %+v", s.Failures)
	}
}

func TestParseCargoAndAllPass(t *testing.T) {
	out := "test math::adds ... ok\ntest math::div_by_zero ... FAILED\n\ntest result: FAILED. 1 passed; 1 failed; 0 ignored\n"
	s := parseTestOutput("cargo", out, 101)
	if s.Failed != 1 || s.Passed != 1 {
		t.Fatalf("统计不对: %+v", s)
	}
	if len(s.Failures) != 1 || s.Failures[0].Name != "math::div_by_zero" {
		t.Fatalf("用例名不对: %+v", s.Failures)
	}

	ok := parseTestOutput("go", "ok  \texample.com/a\t0.002s\nok  \texample.com/b\t0.003s\n", 0)
	if ok.Failed != 0 || ok.Passed != 2 || ok.Broken {
		t.Fatalf("全通过解析异常: %+v", ok)
	}
	if !strings.Contains(ok.render("go", "go test ./...", 0, "", false), "结论: 通过") {
		t.Fatal("结论应为通过")
	}
}

func TestRenderTrimsOriginalOutput(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("noise line\n")
	}
	out := b.String() + goFailOut
	text := parseTestOutput("go", out, 1).render("go", "go test ./...", 1, out, false)
	if strings.Count(text, "noise line") > 30 {
		t.Fatalf("原始输出未裁剪，行数 %d", strings.Count(text, "noise line"))
	}
	if !strings.Contains(text, "失败用例") {
		t.Fatal("摘要缺少失败用例段")
	}
}

func TestDetectFrameworkAndCommand(t *testing.T) {
	dir := t.TempDir()
	if got := detectFramework(dir); got != "" {
		t.Fatalf("空目录不应识别出框架，实得 %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectFramework(dir); got != "go" {
		t.Fatalf("应识别为 go，实得 %q", got)
	}
	cmd, ok := testCommand("go", "-run TestAdd", "")
	if !ok || !strings.Contains(cmd, "-run TestAdd") {
		t.Fatalf("命令拼装异常: %q %v", cmd, ok)
	}
	if _, ok := testCommand("ruby", "", ""); ok {
		t.Fatal("未知框架应返回 false")
	}
}
