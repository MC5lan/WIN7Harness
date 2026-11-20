package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagnosticsFindsGoError(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go 不在 PATH：%v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module diag\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 类型错误：返回值数量不匹配，go vet 会报出来。
	src := "package diag\n\nfunc F() int { return \"x\" }\n"
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := runDiagnostics(&Context{WorkDir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "f.go") {
		t.Fatalf("诊断没有定位到 f.go:\n%s", res.Output)
	}
}
