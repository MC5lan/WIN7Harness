package tools

import (
	"strings"
	"testing"
)

// UNC 路径必须在任何 Stat/ReadDir 之前要求确认；没有确认通道直接拒绝。
func TestWriteFileRejectsUNCWithoutConfirm(t *testing.T) {
	_, err := runWriteFile(&Context{WorkDir: t.TempDir(), Confirm: nil}, map[string]interface{}{
		"path":    `\\server\share\a.txt`,
		"content": "x",
	})
	if err == nil || !strings.Contains(err.Error(), "UNC") {
		t.Fatalf("UNC 写入应被拒绝，实际 %v", err)
	}
}

func TestReadFileRejectsUNCWithoutConfirm(t *testing.T) {
	_, err := runReadFile(&Context{WorkDir: t.TempDir(), Confirm: nil}, map[string]interface{}{
		"path": `\\server\share\a.txt`,
	})
	if err == nil || !strings.Contains(err.Error(), "UNC") {
		t.Fatalf("UNC 读取应被拒绝，实际 %v", err)
	}
}

func TestApplyPatchRejectsUNCWithoutConfirm(t *testing.T) {
	body := "*** Begin Patch\n*** Update File: \\\\server\\share\\a.txt\n@@\n-x\n+y\n*** End Patch"
	_, err := runApplyPatch(&Context{WorkDir: t.TempDir(), Confirm: nil}, map[string]interface{}{
		"patch": body,
	})
	if err == nil || !strings.Contains(err.Error(), "UNC") {
		t.Fatalf("UNC 补丁应被拒绝，实际 %v", err)
	}
}
