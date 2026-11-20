//go:build windows

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaseInsensitiveCollision(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if other, ok := CaseInsensitiveCollision(filepath.Join(dir, "README.md")); ok {
		t.Fatalf("精确大小写不应冲突：%s", other)
	}
	if other, ok := CaseInsensitiveCollision(filepath.Join(dir, "readme.md")); !ok || other != "README.md" {
		t.Fatalf("大小写冲突未识别：%q %v", other, ok)
	}
	if _, ok := CaseInsensitiveCollision(filepath.Join(dir, "other.md")); ok {
		t.Fatal("不同文件名误报冲突")
	}
}

func TestLongPathRoundTrip(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 8; i++ {
		dir = filepath.Join(dir, strings.Repeat("long-segment-", 3))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Skipf("Win7 当前卷不支持长路径：%v", err)
	}
	p := filepath.Join(dir, "long.txt")
	if err := os.WriteFile(p, []byte("long-path-ok"), 0o644); err != nil {
		t.Fatalf("长路径写入失败：%v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "long-path-ok" {
		t.Fatalf("长路径读回失败：%q %v", b, err)
	}
}

func TestValidateWindowsPathSyntax(t *testing.T) {
	ok := []string{
		`C:\work\a.txt`,
		`C:\work\文件夹\a.tar.gz`,
		`\\server\share\dir\a.txt`,
	}
	for _, p := range ok {
		if err := ValidateWindowsPathSyntax(p); err != nil {
			t.Fatalf("%s 应允许：%v", p, err)
		}
	}
	bad := []string{
		`C:\work\a.txt:secret`,
		`C:\work\CON`,
		`C:\work\nul.txt`,
		`C:\work\LPT1.log`,
		`C:\work\a. `,
		`C:\work\a*b.txt`,
		`C:\work\a?b.txt`,
		`\\?\GLOBALROOT\Device\HarddiskVolume1\a`,
	}
	for _, p := range bad {
		if err := ValidateWindowsPathSyntax(p); err == nil {
			t.Fatalf("%s 应拒绝", p)
		}
	}
}
