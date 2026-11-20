package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func signedTestManifest(t *testing.T) (*UpdateManifest, ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	m := &UpdateManifest{
		Version: "1.36.0",
		Notes:   "signed test manifest",
		Files: map[string]UpdateFile{
			"amd64": {
				URL:    "https://example.com/WIN7Harness-amd64.exe",
				SHA256: strings.Repeat("a", 64),
				Size:   12345678,
			},
		},
	}
	if err := SignUpdateManifest(m, priv); err != nil {
		t.Fatal(err)
	}
	return m, priv, base64.StdEncoding.EncodeToString(pub)
}

func TestUpdateManifestSignatureRoundTrip(t *testing.T) {
	m, _, pub := signedTestManifest(t)
	old := UpdatePublicKey
	UpdatePublicKey = pub
	defer func() { UpdatePublicKey = old }()

	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	fetch := func(string) ([]byte, error) { return body, nil }
	got, newer, err := CheckUpdate("1.35.0", "http://example.com/latest.json", fetch)
	if err != nil {
		t.Fatalf("签名清单被拒绝：%v", err)
	}
	if !newer || got.Version != "1.36.0" {
		t.Fatalf("newer=%v version=%q", newer, got.Version)
	}
}

func TestUpdateManifestTamperRejected(t *testing.T) {
	m, _, pub := signedTestManifest(t)
	old := UpdatePublicKey
	UpdatePublicKey = pub
	defer func() { UpdatePublicKey = old }()

	// 签名后改版本号：SHA/URL 都没变，但 canonical payload 变了。
	m.Version = "9.9.9"
	body, _ := json.Marshal(m)
	fetch := func(string) ([]byte, error) { return body, nil }
	if _, _, err := CheckUpdate("1.35.0", "http://example.com/latest.json", fetch); err == nil {
		t.Fatal("篡改后的清单应验签失败")
	}
}

func TestUpdateManifestMissingSignatureRejectedWhenKeySet(t *testing.T) {
	m, _, pub := signedTestManifest(t)
	m.Signature = ""
	old := UpdatePublicKey
	UpdatePublicKey = pub
	defer func() { UpdatePublicKey = old }()

	if err := VerifyUpdateManifest(m, pub); err == nil {
		t.Fatal("缺签名应失败")
	}
}

func TestVerifyUpdateManifestRejectsBadKey(t *testing.T) {
	m, _, _ := signedTestManifest(t)
	if err := VerifyUpdateManifest(m, "not-base64"); err == nil {
		t.Fatal("非法公钥应失败")
	}
}

func TestSignUpdateManifestRejectsShortKey(t *testing.T) {
	m := &UpdateManifest{Version: "1.36.0"}
	if err := SignUpdateManifest(m, ed25519.PrivateKey{1, 2, 3}); err == nil {
		t.Fatal("短私钥应失败")
	}
}
