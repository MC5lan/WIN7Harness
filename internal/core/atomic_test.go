package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 原子写：正常覆盖、以及原文件在写失败时不被破坏。
func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("旧内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("新内容"), 0o644); err != nil {
		t.Fatalf("原子写失败: %v", err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "新内容" {
		t.Fatalf("内容不对: %q", got)
	}
	// 临时文件不能残留
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".~w7h-") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}
}

func TestWriteFileAtomicToMissingDir(t *testing.T) {
	if err := WriteFileAtomic(filepath.Join(t.TempDir(), "no", "such", "a.txt"), []byte("x"), 0o644); err == nil {
		t.Fatal("目录不存在时应报错")
	}
}

// 写失败时不留快照，避免 /undo 出现"回退了但没区别"的一步。
func TestDiscardLastSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", home)

	p := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(p, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotFile(p); err != nil {
		t.Fatal(err)
	}
	if n := SnapshotCount(); n != 1 {
		t.Fatalf("快照数应为 1，实际 %d", n)
	}
	DiscardLastSnapshot()
	if n := SnapshotCount(); n != 0 {
		t.Fatalf("丢弃后应为 0，实际 %d", n)
	}
}

// 局部回滚：一次操作写了 3 个文件，第 3 个失败时把前 2 个退回去，
// 且只消耗这 2 个文件自己的快照，不影响其他历史。
func TestRestoreLatestFor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", home)

	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(a, []byte("a-原始"), 0o644); err != nil {
		t.Fatal(err)
	}
	// b 原本不存在，回滚后应被删除

	if err := SnapshotFile(a); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(a, []byte("a-已改"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotFile(b); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(b, []byte("b-新建"), 0o644); err != nil {
		t.Fatal(err)
	}

	ok, err := RestoreLatestFor(b)
	if err != nil || !ok {
		t.Fatalf("回滚 b 失败: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Fatal("b 是本次新建的，回滚后应被删除")
	}

	ok, err = RestoreLatestFor(a)
	if err != nil || !ok {
		t.Fatalf("回滚 a 失败: ok=%v err=%v", ok, err)
	}
	got, _ := os.ReadFile(a)
	if string(got) != "a-原始" {
		t.Fatalf("a 未恢复: %q", got)
	}

	if n := SnapshotCount(); n != 0 {
		t.Fatalf("回滚消耗后快照应为 0，实际 %d", n)
	}
	// 没有该文件的快照时返回 false，不报错
	ok, err = RestoreLatestFor(a)
	if ok || err != nil {
		t.Fatalf("无可用快照时应返回 false, nil；实际 ok=%v err=%v", ok, err)
	}
}
