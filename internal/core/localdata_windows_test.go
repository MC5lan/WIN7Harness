//go:build windows

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLocalDataMovesSessions(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("WIN7HARNESS_HOME", "")
	t.Setenv("WIN7HARNESS_PORTABLE", "0")

	oldRoot, newRoot := DataDir(), LocalDataDir()
	if samePath(oldRoot, newRoot) {
		t.Skip("当前环境 DataDir 与 LocalDataDir 相同")
	}
	src := filepath.Join(oldRoot, "sessions", "demo", "session.json")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte(`{"id":"demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	MigrateLocalData()

	dst := filepath.Join(newRoot, "sessions", "demo", "session.json")
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("迁移后文件不存在：%v", err)
	}
	if string(b) != `{"id":"demo"}` {
		t.Fatalf("迁移内容不一致：%q", b)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("旧位置仍存在：%v", err)
	}
}
