package core

import (
	"os"
	"path/filepath"
	"testing"
)

// 默认情况（无环境变量、exe 同级无标记文件）不应进入便携模式。
func TestPortableModeDefaultOff(t *testing.T) {
	os.Unsetenv("WIN7HARNESS_PORTABLE")
	if PortableMode() {
		t.Fatalf("未打标记却进入了便携模式")
	}
}

// 环境变量 WIN7HARNESS_PORTABLE=1 必须强制开启便携模式。
func TestPortableModeByEnv(t *testing.T) {
	t.Setenv("WIN7HARNESS_PORTABLE", "1")
	if !PortableMode() {
		t.Fatalf("环境变量未生效")
	}
	t.Setenv("WIN7HARNESS_PORTABLE", "0")
	if PortableMode() {
		t.Fatalf("值为 0 时不应开启")
	}
}

// exe 同级存在标记文件时才开启，且 DataDir 指向同级 data 目录。
func TestPortableModeByFlagFile(t *testing.T) {
	os.Unsetenv("WIN7HARNESS_PORTABLE")
	dir := ExeDir()
	flag := filepath.Join(dir, "portable.flag")
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Skipf("测试目录不可写，跳过：%v", err)
	}
	defer os.Remove(flag)
	defer os.Remove(filepath.Join(dir, "data", ".writable"))

	if !PortableMode() {
		t.Fatalf("标记文件存在但未进入便携模式")
	}
	os.Remove(flag)
	t.Setenv("WIN7HARNESS_HOME", filepath.Join(dir, "home"))
	if got := DataDir(); got != filepath.Join(dir, "home") {
		t.Fatalf("WIN7HARNESS_HOME 应优先于便携模式，实际 %s", got)
	}
}
