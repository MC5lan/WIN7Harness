package llm

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
)

func TestBundledRootsParsed(t *testing.T) {
	cfg, err := NewTLSConfig(TLSOptions{})
	if err != nil {
		t.Fatalf("默认 TLS 配置构建失败：%v", err)
	}
	if cfg.RootCAs == nil {
		t.Fatal("默认配置应带根证书池")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("最低版本应为 TLS1.2，实际 %x", cfg.MinVersion)
	}
	// 内置兜底至少要覆盖主流公共 CA，否则合并没意义。
	if n := len(cfg.RootCAs.Subjects()); n < 20 {
		t.Fatalf("根证书池仅 %d 张，内置兜底可能未生效", n)
	}
	if cfg.InsecureSkipVerify {
		t.Fatal("默认不应跳过证书校验")
	}
}

func TestTLSInsecure(t *testing.T) {
	cfg, err := NewTLSConfig(TLSOptions{Insecure: true})
	if err != nil {
		t.Fatalf("构建失败：%v", err)
	}
	if !cfg.InsecureSkipVerify {
		t.Fatal("Insecure 选项未生效")
	}
}

func TestTLSInvalidExtraCA(t *testing.T) {
	if _, err := NewTLSConfig(TLSOptions{ExtraCAFile: "/definitely/not/here.pem"}); err == nil {
		t.Fatal("文件不存在时应报错")
	}

	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewTLSConfig(TLSOptions{ExtraCAFile: bad}); err == nil {
		t.Fatal("无可用证书时应报错")
	}
}

func TestTLSExtraCAAppended(t *testing.T) {
	base, err := NewTLSConfig(TLSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	before := len(base.RootCAs.Subjects())

	dir := t.TempDir()
	f := filepath.Join(dir, "ca.pem")
	// 复用内置的第一张证书当"用户补充证书"，验证追加路径可用。
	block, _ := nextCertBlock(bundledRoots)
	if block == nil {
		t.Fatal("内置证书读取失败")
	}
	if err := os.WriteFile(f, block, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := NewTLSConfig(TLSOptions{ExtraCAFile: f})
	if err != nil {
		t.Fatalf("补充证书加载失败：%v", err)
	}
	after := len(cfg.RootCAs.Subjects())
	if after < before {
		t.Fatalf("追加后证书数不应减少：%d -> %d", before, after)
	}
}

func TestNewWithTLSBadCA(t *testing.T) {
	if _, err := NewWithTLS("https://api.example.com/v1", "k", "m", 0, TLSOptions{ExtraCAFile: "/nope.pem"}); err == nil {
		t.Fatal("补充证书读取失败时应返回错误")
	}
	c, err := NewWithTLS("https://api.example.com/v1", " k ", "m", 0, TLSOptions{})
	if err != nil {
		t.Fatalf("正常创建失败：%v", err)
	}
	if c.Timeout <= 0 {
		t.Fatal("超时应兜底为默认值")
	}
	if c.APIKey != "k" {
		t.Fatalf("APIKey 未去空格：%q", c.APIKey)
	}
}
