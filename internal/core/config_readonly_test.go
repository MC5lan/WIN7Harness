package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resetConfigLoadState(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		cfgMu.Lock()
		current = nil
		configReadOnly = false
		lastConfigErr = ""
		cfgMu.Unlock()
	})
}

// 读取 config.json 失败时不能当成“没有配置”：内存里是默认值，
// 关闭程序时若自动保存就会覆盖用户原配置。这里验证只读保护。
func TestLoadReadFailureIsReadOnly(t *testing.T) {
	resetConfigLoadState(t)
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	// 用同名目录制造稳定的 ReadFile 失败（比权限/锁文件更可移植）。
	if err := os.Mkdir(filepath.Join(dir, "config.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if cfg == nil {
		t.Fatal("Load() 返回 nil")
	}
	if !ConfigReadOnly() {
		t.Fatal("读取失败后应进入只读保护")
	}
	if msg := ConfigLoadError(); !strings.Contains(msg, "读取失败") {
		t.Fatalf("应给出读取失败提示，实际 %q", msg)
	}
	if err := cfg.Save(); err == nil {
		t.Fatal("只读保护下 Save 应失败")
	}
}

// 0 字节配置通常是写盘中断/杀软清空；同样禁止自动保存。
func TestLoadEmptyFileIsReadOnly(t *testing.T) {
	resetConfigLoadState(t)
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if cfg == nil {
		t.Fatal("Load() 返回 nil")
	}
	if !ConfigReadOnly() {
		t.Fatal("空配置应进入只读保护")
	}
	if err := cfg.Save(); err == nil {
		t.Fatal("只读保护下 Save 应失败")
	}
}
