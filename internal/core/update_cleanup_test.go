package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 最新一份 .old 必须无条件保留：上一版 exe 的 mtime 可能是几个月前，
// 按 mtime 清理会把唯一回滚副本删掉。
func TestCleanupOldExeKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	self, err := SelfExePath()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(self)
	newest := filepath.Join(dir, base+".old")
	older := filepath.Join(dir, base+".old.bak.old")
	for _, p := range []string{newest, older} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	removed := CleanupOldExe(dir)
	if removed != 1 {
		t.Fatalf("应清理 1 份旧副本，实际 %d", removed)
	}
	if _, err := os.Stat(newest); err != nil {
		t.Fatalf("最新回滚副本被误删：%v", err)
	}
	if _, err := os.Stat(older); !os.IsNotExist(err) {
		t.Fatalf("40 天前的旧副本应被清理：%v", err)
	}
}
