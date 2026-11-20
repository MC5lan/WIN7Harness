package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigEncryptsAPIKeyAtRest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)

	cfg := DefaultConfig()
	cfg.Providers[cfg.ActiveProvider].APIKey = "sk-dpapi-test-123456"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-dpapi-test-123456") {
		t.Fatalf("config.json 里仍有明文 API Key：%s", raw)
	}
	if !strings.Contains(string(raw), "api_key_enc") {
		t.Fatalf("config.json 缺少 api_key_enc：%s", raw)
	}

	loaded := Load()
	if got := loaded.Providers[loaded.ActiveProvider].APIKey; got != "sk-dpapi-test-123456" {
		t.Fatalf("Load 后 Key 未还原：%q", got)
	}
}

func TestLegacyPlaintextKeyMigratesOnSave(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)

	legacy := `{
  "version": "1.35.0",
  "active_provider": 0,
  "providers": [
    {"id":"p","name":"P","base_url":"https://example.com/v1","api_key":"sk-legacy-123456","model":"m"}
  ],
  "model": "m"
}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load()
	if got := cfg.Providers[0].APIKey; got != "sk-legacy-123456" {
		t.Fatalf("旧明文 Key 未加载：%q", got)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-legacy-123456") {
		t.Fatalf("保存后仍是明文：%s", raw)
	}
	again := Load()
	if got := again.Providers[0].APIKey; got != "sk-legacy-123456" {
		t.Fatalf("迁移后 Key 未还原：%q", got)
	}
}

func TestCorruptEncryptedKeyPromptsUser(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	SetStartupWarning("")

	body := `{
  "version": "1.36.0",
  "active_provider": 0,
  "providers": [
    {"id":"p","name":"P","base_url":"https://example.com/v1","api_key_enc":"dpapi:v1:AAAA","model":"m"}
  ],
  "model": "m"
}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Load()
	if cfg.Providers[0].APIKey != "" {
		t.Fatal("损坏密文不应被当成明文 Key")
	}
	if !strings.Contains(StartupWarning(), "API Key") {
		t.Fatalf("没有提示用户重填 Key：%q", StartupWarning())
	}
}

func TestRedactedConfigDropsCiphertext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)

	cfg := DefaultConfig()
	cfg.Providers[cfg.ActiveProvider].APIKey = "sk-redact-123456"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	Load()
	out := RedactedConfigJSON()
	if strings.Contains(out, "api_key_enc") {
		t.Fatalf("支持包不应包含密文：%s", out)
	}
	if strings.Contains(out, "sk-redact-123456") {
		t.Fatalf("支持包不应包含明文 Key：%s", out)
	}
}
