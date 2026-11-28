package core

import "testing"

// 成功解密后清掉内存密文：用户随后清空 Key 时，forDisk 才能识别为删除，
// 而不是把旧密文继续写回去。
func TestDecryptProviderKeysClearsEncAfterSuccess(t *testing.T) {
	enc, err := sealSecret("sk-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Providers: []Provider{{ID: "p", APIKeyEnc: enc}}}
	cfg.decryptProviderKeys()
	p := cfg.Providers[0]
	if p.APIKey != "sk-test-secret" {
		t.Fatalf("明文未还原：%q", p.APIKey)
	}
	if p.APIKeyEnc != "" {
		t.Fatalf("成功解密后应清掉内存密文，实际 %q", p.APIKeyEnc)
	}
	if !p.KeyLoaded {
		t.Fatal("KeyLoaded 应为 true")
	}
}

// 解密失败必须保留密文：配置拷贝到别的机器/用户时，原密文不能因为
// 一次加载失败就被后续保存永久抹掉。
func TestDecryptProviderKeysKeepsEncOnFailure(t *testing.T) {
	cfg := &Config{Providers: []Provider{{ID: "p", APIKeyEnc: "not-a-valid-secret"}}}
	cfg.decryptProviderKeys()
	p := cfg.Providers[0]
	if p.APIKeyEnc != "not-a-valid-secret" {
		t.Fatalf("解密失败后密文被改动：%q", p.APIKeyEnc)
	}
	if p.APIKey != "" || p.KeyLoaded {
		t.Fatalf("解密失败不应标记已加载：key=%q loaded=%v", p.APIKey, p.KeyLoaded)
	}
}
