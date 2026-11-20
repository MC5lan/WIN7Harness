package core

import "testing"

// TestPresetProvidersNoModelNames 锁定 v1.32 的关键约束：
// 只内置接入方式（OpenCode Go / 自定义 / 本地），不内置任何模型名。
func TestPresetProvidersNoModelNames(t *testing.T) {
	ps := PresetProviders()
	if len(ps) != 3 {
		t.Fatalf("预设应为 3 个接入方式，实际 %d", len(ps))
	}
	if ps[0].ID != "opencode-go" {
		t.Fatalf("首个预设应为 opencode-go，实际 %s", ps[0].ID)
	}
	if ps[0].BaseURL != OpenCodeGoBaseURL {
		t.Fatalf("OpenCode Go 端点错误: %s", ps[0].BaseURL)
	}
	wantIDs := map[string]bool{"opencode-go": true, "custom": true, "local": true}
	for _, p := range ps {
		if p.ID == "" || p.Name == "" {
			t.Fatalf("预设字段不完整: %+v", p)
		}
		if !wantIDs[p.ID] {
			t.Fatalf("出现未预期的内置端点 %s", p.ID)
		}
		if p.APIKey != "" {
			t.Fatalf("预设不得内置密钥: %s", p.ID)
		}
		if p.Model != "" || len(p.Models) != 0 {
			t.Fatalf("预设不得内置模型名: %s → model=%q models=%v", p.ID, p.Model, p.Models)
		}
	}
	if ps[1].BaseURL != "" {
		t.Fatalf("自定义端点应留空，实际 %s", ps[1].BaseURL)
	}
	if !ps[2].Local || !ps[2].NoAuth {
		t.Fatalf("本地端点应标记 local/no_auth: %+v", ps[2])
	}
}

// TestDefaultConfigHasNoModel 默认配置不猜模型，必须由用户填写或动态拉取。
func TestDefaultConfigHasNoModel(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.PresetVersion != PresetVersion {
		t.Fatalf("PresetVersion=%d, want %d", cfg.PresetVersion, PresetVersion)
	}
	if cfg.Model != "" {
		t.Fatalf("默认配置不应内置模型名，实际 %q", cfg.Model)
	}
	if len(cfg.Providers) != 3 {
		t.Fatalf("默认配置应带 3 个接入预设，实际 %d", len(cfg.Providers))
	}
}

// TestMergeConfigKeepsUserProvider 用户已有配置原样保留，新预设只补缺。
func TestMergeConfigKeepsUserProvider(t *testing.T) {
	def := DefaultConfig()
	saved := &Config{
		PresetVersion: 0,
		Providers: []Provider{{
			ID: "deepseek", Name: "公司网关", BaseURL: "https://gw.example.com/v1",
			APIKey: "sk-keep", Model: "my-model",
		}},
		ActiveProvider: 0,
		Model:          "my-model",
	}
	got := mergeConfig(def, saved)
	p := got.Providers[0]
	if p.BaseURL != "https://gw.example.com/v1" || p.Model != "my-model" || p.APIKey != "sk-keep" {
		t.Fatalf("用户配置被改写: %+v", p)
	}
	if got.Model != "my-model" {
		t.Fatalf("顶层模型被改写: %s", got.Model)
	}
	if got.PresetVersion != PresetVersion {
		t.Fatalf("PresetVersion 未写入: %d", got.PresetVersion)
	}
	for _, id := range []string{"opencode-go", "custom", "local"} {
		found := false
		for _, x := range got.Providers {
			if x.ID == id {
				found = true
			}
		}
		if !found {
			t.Fatalf("新接入预设 %s 未补齐", id)
		}
	}
}

// TestNormalizeOpenCodeGo 兼容新旧控制台域名、末尾斜杠和 opencode-go/ 前缀。
func TestNormalizeOpenCodeGo(t *testing.T) {
	cases := map[string]string{
		"https://opencode.ai/zen/go":                           "https://opencode.ai/zen/go/v1",
		"https://opencode.ai/zen/go/v1/":                       "https://opencode.ai/zen/go/v1",
		"https://opencode.ai/inference/go":                     "https://opencode.ai/inference/go/v1",
		"https://opencode.ai/inference/go/v1/chat/completions": "https://opencode.ai/inference/go/v1/chat/completions",
	}
	for in, want := range cases {
		if got := NormalizeBaseURL(in); got != want {
			t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
	if got := NormalizeModelID(OpenCodeGoBaseURL, "opencode-go/kimi-k3"); got != "kimi-k3" {
		t.Fatalf("opencode-go 前缀未去掉: %q", got)
	}
	if got := NormalizeModelID("https://gw.example.com/v1", "opencode-go/kimi-k3"); got != "opencode-go/kimi-k3" {
		t.Fatalf("自定义网关的命名空间不应被改写: %q", got)
	}
}
