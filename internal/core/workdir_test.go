package core

import (
	"path/filepath"
	"testing"
)

func TestValidateWorkDirRejectsProgramAndDataDirs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)

	if err := ValidateWorkDir(dir); err == nil {
		t.Fatal("数据目录不应被当成工作区")
	}
	if err := ValidateWorkDir(filepath.Join(dir, "projects")); err == nil {
		t.Fatal("数据目录的子目录也不应被当成工作区")
	}
	if err := ValidateWorkDir(ExeDir()); err == nil {
		t.Fatal("程序目录不应被当成工作区")
	}

	ok := t.TempDir()
	if err := ValidateWorkDir(ok); err != nil {
		t.Fatalf("普通目录应通过，实际 %v", err)
	}
}
