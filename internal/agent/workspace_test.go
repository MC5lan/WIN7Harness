package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceSummaryListsProjectMarker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := workspaceSummary(dir)
	for _, want := range []string{"工作区概览", "main.go", "go.mod", "module=example.com/demo"} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary 缺少 %q:\n%s", want, s)
		}
	}
}

func TestWorkspaceSummaryEmptyDir(t *testing.T) {
	s := workspaceSummary(t.TempDir())
	if !strings.Contains(s, "（空目录）") {
		t.Fatalf("空目录摘要异常:\n%s", s)
	}
}
