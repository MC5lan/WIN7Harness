package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectDocAndSnapshot(t *testing.T) {
	root := t.TempDir()
	os.Setenv("WIN7HARNESS_HOME", filepath.Join(root, "home"))
	sub := filepath.Join(root, "proj", "src")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(root, "proj", "AGENTS.md"), []byte("# 约定\n必须用 tabs，禁止引入三方依赖。"), 0o644)

	block := ProjectDocBlock(sub)
	if !contains(block, "禁止引入三方依赖") || !contains(block, "AGENTS.md") {
		t.Fatalf("项目约定未注入: %q", block)
	}
	// 改文件后缓存必须失效，不能继续注入旧约定。
	os.WriteFile(filepath.Join(root, "proj", "AGENTS.md"), []byte("# 约定\n必须用 spaces，禁止引入内部包。"), 0o644)
	block = ProjectDocBlock(sub)
	if !contains(block, "禁止引入内部包") || contains(block, "禁止引入三方依赖") {
		t.Fatalf("项目约定缓存未失效: %q", block)
	}

	// 快照：修改已有文件后回滚
	f := filepath.Join(root, "proj", "a.txt")
	os.WriteFile(f, []byte("v1"), 0o644)
	if err := SnapshotFile(f); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(f, []byte("v2"), 0o644)
	res, err := UndoLast()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(f)
	if string(got) != "v1" || res.Path != f {
		t.Fatalf("回滚失败: %s / %q", res.Path, string(got))
	}

	// 快照：撤销新建文件（应删除）
	n := filepath.Join(root, "proj", "b.txt")
	if err := SnapshotFile(n); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(n, []byte("new"), 0o644)
	if _, err := UndoLast(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(n); !os.IsNotExist(err) {
		t.Fatalf("新建文件未被撤销: %v", err)
	}
	if SnapshotCount() != 0 {
		t.Fatalf("快照未清理: %d", SnapshotCount())
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
