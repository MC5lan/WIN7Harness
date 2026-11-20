package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoMapSummarizesGoModule(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/demo\n\ngo 1.20\n")
	write("cmd/app/main.go", "package main\nfunc main(){}\n")
	write("internal/store/store.go", "package store\n")
	write("internal/store/store_test.go", "package store\n")
	write("README.md", "demo\n")

	res, err := runRepoMap(&Context{WorkDir: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"example.com/demo", "cmd/app", "[main]", "internal/store"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("repo_map 缺少 %q:\n%s", want, res.Output)
		}
	}
}
