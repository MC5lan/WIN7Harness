package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// 递归检索必须拒绝盘符根/用户根/系统根：这些目录会拖垮 UI，也是这次
// “跑任务突然闪退”的直接触发点（模型 glob 了 C:\Users）。
func TestGuardSearchRootRejectsBroadRoots(t *testing.T) {
	cases := []string{
		`C:\`,
		`C:\Users`,
		`C:\Windows`,
		`C:\Program Files`,
		`C:\ProgramData`,
	}
	if up := os.Getenv("USERPROFILE"); up != "" {
		cases = append(cases, up)
	}
	for _, root := range cases {
		if err := guardSearchRoot(root); err == nil {
			t.Fatalf("guardSearchRoot(%q) 应拒绝", root)
		}
	}
}

func TestGuardSearchRootAllowsProjectDir(t *testing.T) {
	dir := t.TempDir()
	if err := guardSearchRoot(dir); err != nil {
		t.Fatalf("工作区子目录应允许: %v", err)
	}
	if err := guardSearchRoot(filepath.Join(dir, "src")); err != nil {
		t.Fatalf("更深的子目录应允许: %v", err)
	}
}

func TestRunGlobRejectsBroadRoot(t *testing.T) {
	ctx := &Context{WorkDir: t.TempDir()}
	_, err := runGlob(ctx, map[string]interface{}{
		"pattern": "**/anything*",
		"path":    `C:\Users`,
	})
	if err == nil {
		t.Fatal("glob C:\\Users 必须直接拒绝，而不是开始全盘扫描")
	}
}
