package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// diagnostics 会执行 go vet / cargo check，不能用来读取/构建工作区外目录。
func TestDiagnosticsRejectsOutsideRootWithoutConfirm(t *testing.T) {
	inside := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runDiagnostics(&Context{WorkDir: inside, Confirm: nil}, map[string]interface{}{
		"path": outside,
	})
	if err == nil || !strings.Contains(err.Error(), "工作区之外") {
		t.Fatalf("工作区外 diagnostics 应被拒绝，实际 %v", err)
	}
}

// LSP 也不能对工作区外文件静默启动语言服务器。
func TestLSPRejectsOutsideFileWithoutConfirm(t *testing.T) {
	inside := t.TempDir()
	outside := t.TempDir()
	p := filepath.Join(outside, "a.go")
	if err := os.WriteFile(p, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runLSP(&Context{WorkDir: inside, Confirm: nil}, map[string]interface{}{
		"action": "hover",
		"path":   p,
		"line":   1,
		"column": 1,
	})
	if err == nil || !strings.Contains(err.Error(), "工作区之外") {
		t.Fatalf("工作区外 LSP 应被拒绝，实际 %v", err)
	}
}
