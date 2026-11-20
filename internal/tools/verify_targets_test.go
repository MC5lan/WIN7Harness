package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGoPackageArgs(t *testing.T) {
	cwd := filepath.Join(`C:\work`)
	cases := []struct {
		name    string
		changed []string
		want    string
	}{
		{"two packages", []string{`C:\work\internal\a\a.go`, `internal\b\b.go`}, "./internal/a ./internal/b"},
		{"root package", []string{`main.go`}, "."},
		{"non-go falls back full", []string{`README.md`}, ""},
		{"go.mod falls back full", []string{`go.mod`}, ""},
		{"outside falls back full", []string{`C:\other\x.go`}, ""},
		{"empty falls back full", nil, ""},
	}
	for _, c := range cases {
		if got := goPackageArgs(cwd, c.changed); got != c.want {
			t.Fatalf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// 受影响包验证：只改 b 时，不应因为 a 的失败测试而判红；全量验证才判红。
func TestVerifyProjectTargetsOnlyRunsChanged(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go 不在 PATH：%v", err)
	}
	root := t.TempDir()
	writeF := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeF("go.mod", "module vt\n\ngo 1.20\n")
	writeF("a/a.go", "package a\n")
	writeF("a/a_test.go", "package a\nimport \"testing\"\nfunc TestA(t *testing.T){ t.Fatal(\"boom\") }\n")
	writeF("b/b.go", "package b\nfunc OK() bool { return true }\n")
	writeF("b/b_test.go", "package b\nimport \"testing\"\nfunc TestB(t *testing.T){ if !OK(){ t.Fatal(\"bad\") } }\n")

	targeted := VerifyProjectTargets(&Context{WorkDir: root}, root, "", 60*time.Second, []string{"b/b.go"})
	if !targeted.OK {
		t.Fatalf("受影响包验证应通过，实际: %s", targeted.Report)
	}
	full := VerifyProject(&Context{WorkDir: root}, root, "", 60*time.Second)
	if full.OK {
		t.Fatal("全量验证应因 a 的失败测试判红")
	}
}

func TestExpandGoTargetsIncludesDependent(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go 不在 PATH：%v", err)
	}
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module deps\n\ngo 1.20\n")
	write("a/a.go", "package a\nfunc Value() int { return 1 }\n")
	write("b/b.go", "package b\nimport \"deps/a\"\nfunc Use() int { return a.Value() }\n")
	got := expandGoTargets(root, []string{"a/a.go"})
	if !strings.Contains(got, "./a") || !strings.Contains(got, "./b") {
		t.Fatalf("反向依赖未展开: %q", got)
	}
}

func TestVerifyProjectTargetsIncludesDependentTests(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go 不在 PATH：%v", err)
	}
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module rev\n\ngo 1.20\n")
	write("a/a.go", "package a\nfunc Value() int { return 1 }\n")
	write("b/b.go", "package b\nimport \"rev/a\"\nfunc Use() int { return a.Value() }\n")
	write("b/b_test.go", "package b\nimport \"testing\"\nfunc TestUse(t *testing.T){ if Use()!=2 { t.Fatal(\"want 2\") } }\n")
	res := VerifyProjectTargets(&Context{WorkDir: root}, root, "", 60*time.Second, []string{"a/a.go"})
	if res.OK {
		t.Fatalf("依赖方 b 的失败测试应被选中: %s", res.TestCmd)
	}
}
