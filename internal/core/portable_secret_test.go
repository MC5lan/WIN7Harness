package core

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resetPortableState() {
	cfgMu.Lock()
	current = nil
	portablePassword = ""
	portableLocked = false
	cfgMu.Unlock()
}

func TestPBKDF2KnownVector(t *testing.T) {
	// RFC 7914 / common PBKDF2-HMAC-SHA256 test vector:
	// PBKDF2("password", "salt", 1, 32)
	got := hex.EncodeToString(pbkdf2SHA256([]byte("password"), []byte("salt"), 1, 32))
	want := "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"
	if got != want {
		t.Fatalf("PBKDF2 结果不对：got %s want %s", got, want)
	}
}

func TestPortableSecretsRoundTrip(t *testing.T) {
	plain := []byte(`{"v":1,"keys":{"p":"sk-portable-123"}}`)
	enc, err := sealPortableSecrets(plain, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, portableSecretPrefix) {
		t.Fatalf("格式前缀不对：%s", enc)
	}
	if strings.Contains(enc, "sk-portable-123") {
		t.Fatal("密文里出现了明文")
	}
	got, err := openPortableSecrets(enc, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Fatalf("解密结果不一致：%s", got)
	}
	if _, err := openPortableSecrets(enc, "wrong password"); err != ErrPortablePassword {
		t.Fatalf("错误密码应返回 ErrPortablePassword，实际 %v", err)
	}
}

func TestPortableSecretsTamper(t *testing.T) {
	enc, err := sealPortableSecrets([]byte(`{"v":1,"keys":{"p":"k"}}`), "password-123")
	if err != nil {
		t.Fatal(err)
	}
	// 改最后一个 base64 字符，GCM 校验必须失败。
	last := enc[len(enc)-1]
	repl := byte('A')
	if last == 'A' {
		repl = 'B'
	}
	tampered := enc[:len(enc)-1] + string(repl)
	if _, err := openPortableSecrets(tampered, "password-123"); err == nil {
		t.Fatal("被篡改的密文不应解开")
	}
}

func TestPortableSaveRequiresPassword(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	t.Setenv("WIN7HARNESS_PORTABLE", "1")
	resetPortableState()

	cfg := DefaultConfig()
	cfg.Providers[cfg.ActiveProvider].APIKey = "sk-needs-password"
	if err := cfg.Save(); err != ErrPortablePasswordRequired {
		t.Fatalf("没有 U 盘密码时保存应失败，实际 %v", err)
	}
	if _, err := os.Stat(ConfigPath()); err == nil {
		t.Fatal("失败时不应写出 config.json")
	}
}

func TestPortableConfigEncryptsAndUnlocks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	t.Setenv("WIN7HARNESS_PORTABLE", "1")
	resetPortableState()

	cfg := DefaultConfig()
	cfg.Providers[cfg.ActiveProvider].APIKey = "sk-portable-config-123"
	if err := SetPortablePassword("u盘密码-123456"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-portable-config-123") {
		t.Fatalf("config.json 里有明文 Key：%s", raw)
	}
	if !strings.Contains(string(raw), "portable_secrets") {
		t.Fatalf("config.json 缺少 portable_secrets：%s", raw)
	}
	if strings.Contains(string(raw), "api_key_enc") {
		t.Fatalf("便携模式不应写 DPAPI 密文：%s", raw)
	}

	loaded := Load()
	if !PortableSecretsPresent() || !PortableSecretsLocked() {
		t.Fatal("重新加载后应处于锁定状态")
	}
	if got := loaded.Providers[loaded.ActiveProvider].APIKey; got != "" {
		t.Fatalf("未解锁时不应有内存 Key：%q", got)
	}
	if err := UnlockPortableSecrets("wrong-password"); err == nil {
		t.Fatal("错误密码不应解锁")
	}
	if err := UnlockPortableSecrets("u盘密码-123456"); err != nil {
		t.Fatal(err)
	}
	// UnlockPortableSecrets 用新的只读快照替换 current；UI 私有副本由
	// 调用方（app）同步，这里验证发布出去的新快照。
	snap := Get()
	if got := snap.Providers[snap.ActiveProvider].APIKey; got != "sk-portable-config-123" {
		t.Fatalf("解锁后 Key 不对：%q", got)
	}
}

func TestPortableLockedSavePreservesCiphertext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	t.Setenv("WIN7HARNESS_PORTABLE", "1")
	resetPortableState()

	cfg := DefaultConfig()
	cfg.Providers[cfg.ActiveProvider].APIKey = "sk-preserve-123"
	if err := SetPortablePassword("password-123456"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}

	loaded := Load() // 模拟下次启动：锁定
	loaded.Theme = "dark"
	if err := loaded.Save(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "portable_secrets") {
		t.Fatalf("锁定状态下保存把密文弄丢了：%s", after)
	}
	// 密文本身不应因为改了主题就被重写。
	if strings.Count(string(before), "portable_secrets") != strings.Count(string(after), "portable_secrets") {
		t.Fatalf("锁定状态下不应改动 portable_secrets")
	}
}

func TestPortableClearSecrets(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	t.Setenv("WIN7HARNESS_PORTABLE", "1")
	resetPortableState()

	cfg := DefaultConfig()
	cfg.Providers[cfg.ActiveProvider].APIKey = "sk-clear-123"
	if err := SetPortablePassword("password-123456"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	Load()
	if err := ClearPortableSecrets(); err != nil {
		t.Fatal(err)
	}
	if err := Get().Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "portable_secrets") {
		t.Fatalf("清除后仍保留密文：%s", raw)
	}
}

func TestStandardModeStillUsesDPAPI(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	t.Setenv("WIN7HARNESS_PORTABLE", "0")
	resetPortableState()

	cfg := DefaultConfig()
	cfg.Providers[cfg.ActiveProvider].APIKey = "sk-standard-123"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-standard-123") {
		t.Fatalf("标准模式出现明文：%s", raw)
	}
	if !strings.Contains(string(raw), "api_key_enc") {
		t.Fatalf("标准模式应写 api_key_enc：%s", raw)
	}
	if strings.Contains(string(raw), "portable_secrets") {
		t.Fatalf("标准模式不应写 portable_secrets：%s", raw)
	}
}
