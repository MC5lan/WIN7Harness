//go:build windows

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"win7harness/internal/core"
)

func TestGitRootFindsRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不在 PATH：%v", err)
	}
	dir := t.TempDir()
	if out, err := core.GitRun(dir, "init"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	root, err := gitRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(dir)
	got, _ := filepath.Abs(root)
	if !samePathFold(got, want) {
		t.Fatalf("gitRoot = %q, want %q", got, want)
	}
}

func TestCreateWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不在 PATH：%v", err)
	}
	repo := t.TempDir()
	if out, err := core.GitRun(repo, "init"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := core.GitRun(repo, "add", "."); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := core.GitRun(repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	wt := filepath.Join(t.TempDir(), "isolated")
	if out, err := createWorktree(repo, "harness/test", wt); err != nil {
		t.Fatalf("createWorktree: %v\n%s", err, out)
	}
	defer core.GitRun(repo, "worktree", "remove", "--force", wt)
	if _, err := os.Stat(filepath.Join(wt, "a.txt")); err != nil {
		t.Fatalf("worktree 文件不存在: %v", err)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if out, err := core.GitRun(repo, "init"); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := core.GitRun(repo, "add", "."); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := core.GitRun(repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	return repo
}

func TestCommitAllAndMergeWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不在 PATH：%v", err)
	}
	repo := initGitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := createWorktree(repo, "harness/merge-test", wt); err != nil {
		t.Fatalf("createWorktree: %v\n%s", err, out)
	}
	defer core.GitRun(repo, "worktree", "remove", "--force", wt)
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := commitAll(wt, "feature"); err != nil {
		t.Fatalf("commitAll: %v\n%s", err, out)
	}
	if out, err := core.GitRun(repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "merge", "--no-ff", "--no-edit", "harness/merge-test"); err != nil {
		t.Fatalf("merge: %v\n%s", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(repo, "a.txt"))
	if strings.TrimSpace(string(b)) != "feature" {
		t.Fatalf("merge 后内容 = %q", b)
	}
}

func TestMergeConflictCanAbort(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不在 PATH：%v", err)
	}
	repo := initGitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := createWorktree(repo, "harness/conflict-test", wt); err != nil {
		t.Fatalf("createWorktree: %v\n%s", err, out)
	}
	defer core.GitRun(repo, "worktree", "remove", "--force", wt)
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := commitAll(wt, "feature"); err != nil {
		t.Fatalf("commitAll: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := core.GitRun(repo, "add", "."); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if out, err := core.GitRun(repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-m", "main"); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
	if out, err := core.GitRun(repo, "-c", "user.name=t", "-c", "user.email=t@example.com", "merge", "--no-ff", "--no-edit", "harness/conflict-test"); err == nil {
		t.Fatalf("应冲突: %s", out)
	}
	if conflicts, _ := core.GitRun(repo, "diff", "--name-only", "--diff-filter=U"); !strings.Contains(conflicts, "a.txt") {
		t.Fatalf("应列出冲突文件: %q", conflicts)
	}
	if _, err := core.GitRun(repo, "merge", "--abort"); err != nil {
		t.Fatal("merge --abort 失败")
	}
	if dirty, files := gitDirty(repo); dirty {
		t.Fatalf("abort 后仓库仍不干净: %v", files)
	}
}

func samePathFold(a, b string) bool {
	if a == b {
		return true
	}
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	if aerr != nil || berr != nil {
		return false
	}
	return os.SameFile(ai, bi)
}
