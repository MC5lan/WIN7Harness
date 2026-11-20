package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// 回归测试：Load() 必须完整保留用户保存的 provider（含 API Key）。
// 2026-11-17 的事故：Load() 里 c := &Config{} 遮蔽了外层 c，
// 合并结果只作用到局部变量，函数继续用 DefaultConfig()，用户配置
// （deepseek-flash、Key）在内存里被换成预置端点+空 Key。
func TestLoadKeepsUserProvidersAndKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)

	fixture := map[string]interface{}{
		"version":         "1.29.1",
		"preset_version":  2,
		"onboarded":       true,
		"active_provider": 0,
		"model":           "deepseek-flash",
		"work_dir":        dir,
		"providers": []map[string]interface{}{
			{
				"id":       "deepseek",
				"name":     "DeepSeek",
				"api_key":  "sk-regression-secret-abc123",
				"base_url": "https://api.deepseek.com/v1",
				"model":    "deepseek-flash",
				"models":   []string{"deepseek-flash", "deepseek-v4-flash"},
			},
		},
	}
	writeConfigFixture(t, dir, fixture)

	cfg := Load()
	if cfg == nil {
		t.Fatal("Load() 返回了 nil")
	}
	if len(cfg.Providers) == 0 {
		t.Fatal("Load() 后 Providers 被清空——用户配置丢失")
	}
	for _, p := range cfg.Providers {
		if p.ID == "deepseek" {
			if p.APIKey != "sk-regression-secret-abc123" {
				t.Fatalf("provider deepseek 的 API Key 被清空（len=%d）", len(p.APIKey))
			}
			if p.Model != "deepseek-flash" {
				t.Fatalf("provider deepseek 的 Model 被改成 %q", p.Model)
			}
			return
		}
	}
	t.Fatal("Load() 后找不到用户保存的 deepseek provider")
}

// 坏字段只回退该字段，其余（Key/Model/地址）照常生效。
func TestLoadRepairsBadFieldsKeepsProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)

	// 顶层 budget_tokens 写成字符串，走容错解析
	fixture := map[string]interface{}{
		"version":         "1.30.0",
		"preset_version":  2,
		"active_provider": 0,
		"model":           "deepseek-flash",
		"work_dir":        dir,
		"budget_tokens":   "oops",
		"providers": []map[string]interface{}{
			{
				"id":       "deepseek",
				"name":     "DeepSeek",
				"api_key":  "sk-regression-secret-xyz789",
				"base_url": "https://api.deepseek.com/v1",
				"model":    "deepseek-flash",
				"models":   []string{"deepseek-flash"},
			},
		},
	}
	writeConfigFixture(t, dir, fixture)

	cfg := Load()
	if cfg == nil {
		t.Fatal("Load() 返回了 nil")
	}
	for _, p := range cfg.Providers {
		if p.ID == "deepseek" {
			if p.APIKey != "sk-regression-secret-xyz789" {
				t.Fatalf("容错路径 Key 丢失（len=%d）", len(p.APIKey))
			}
			return
		}
	}
	t.Fatal("容错路径后找不到用户 deepseek provider")
}

// Win7 记事本另存为 UTF-8 会带 BOM。带 BOM 的配置必须照常加载，
// 不能判成损坏整份回退默认（否则用户的 Key、模型与工作目录全丢）。
func TestLoadToleratesUTF8BOM(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)

	raw := append([]byte{0xEF, 0xBB, 0xBF},
		[]byte(`{"onboarded":true,"model":"deepseek-flash","work_dir":"`+filepath.ToSlash(dir)+`"}`)...)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if !cfg.Onboarded {
		t.Fatal("带 BOM 的配置被丢弃：onboarded 回退成 false")
	}
	if cfg.Model != "deepseek-flash" {
		t.Fatalf("带 BOM 的配置被丢弃：model=%q", cfg.Model)
	}
	if msg := TakeConfigLoadError(); msg != "" {
		t.Fatalf("带 BOM 的配置不应报损坏，实际：%s", msg)
	}
	// 也不应把好配置隔离成 .bad 备份。
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "config.json" {
			t.Fatalf("带 BOM 的配置被隔离或改写：%s", e.Name())
		}
	}
}

func writeConfigFixture(t *testing.T, dir string, fixture map[string]interface{}) {
	t.Helper()
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
