package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLSPGoDiagnostics(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skipf("gopls 不在 PATH：%v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module lspdiag\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file, []byte("package main\n\nfunc main() { var x int = \"bad\"; _ = x }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := runLSP(&Context{WorkDir: dir}, map[string]interface{}{"action": "diagnostics", "path": file})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "cannot use") {
		t.Fatalf("LSP 没有返回类型错误:\n%s", res.Output)
	}
}
