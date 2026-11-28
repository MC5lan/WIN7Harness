//go:build windows

package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestGitHubLatestURLRejectsInjection(t *testing.T) {
	for _, bad := range []string{"", "owner", "a/b/c", "a/../b", "https://evil.com/x", "a b/c"} {
		if _, err := GitHubLatestURL(bad); err == nil {
			t.Errorf("仓库 %q 应被拒绝", bad)
		}
	}
	got, err := GitHubLatestURL("MC5lan/WIN7Harness")
	if err != nil || got != "https://api.github.com/repos/MC5lan/WIN7Harness/releases/latest" {
		t.Fatalf("URL=%q err=%v", got, err)
	}
}

func TestGitHubSumsParsing(t *testing.T) {
	const sum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	raw := "# comment\n" + sum + "  WIN7Harness-amd64.exe\n" + strings.ToUpper(sum) + " *WIN7Harness-386.exe\nbad line\n"
	got := githubSums([]byte(raw))
	if got["win7harness-amd64.exe"] != sum {
		t.Errorf("amd64 校验和错误：%q", got["win7harness-amd64.exe"])
	}
	if got["win7harness-386.exe"] != sum {
		t.Errorf("386 校验和错误（大小写/星号应归一）：%q", got["win7harness-386.exe"])
	}
}

func TestIsNewerGitHubRelease(t *testing.T) {
	for _, tc := range []struct {
		cur, tag string
		want     bool
	}{
		{"1.40.1", "v1.40.2", true},
		{"1.40.1", "1.40.1", false},
		{"1.40.1", "v1.39.9", false},
		{"1.40.1", "nonsense", true},
	} {
		if got := IsNewerGitHubRelease(tc.cur, tc.tag); got != tc.want {
			t.Errorf("IsNewerGitHubRelease(%q,%q)=%v want %v", tc.cur, tc.tag, got, tc.want)
		}
	}
}

func TestGitHubUpdateFileRequiresChecksum(t *testing.T) {
	rel := &GitHubRelease{TagName: "v1.40.2"}
	rel.Assets = append(rel.Assets, struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	}{Name: "WIN7Harness-amd64.exe", BrowserDownloadURL: "https://example.com/x.exe"})
	if _, err := GitHubUpdateFile(rel, "amd64", func(string) ([]byte, error) { return nil, nil }); err == nil {
		t.Fatal("没有 SHA256SUMS.txt 时必须 fail-closed")
	}
}

// GitHub 模式在正式构建（有 UpdatePublicKey）时必须同时验 latest.json 的签名。
func TestGitHubManifestSignature(t *testing.T) {
	const sum = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	rel := &GitHubRelease{TagName: "v2.0.3"}
	rel.Assets = append(rel.Assets,
		struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{Name: "WIN7Harness-amd64.exe", BrowserDownloadURL: "https://x/exe"},
		struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{Name: "SHA256SUMS.txt", BrowserDownloadURL: "https://x/sums"},
		struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{Name: "latest.json", BrowserDownloadURL: "https://x/latest"},
	)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := UpdatePublicKey
	UpdatePublicKey = base64.StdEncoding.EncodeToString(pub)
	defer func() { UpdatePublicKey = old }()

	m := &UpdateManifest{Version: "2.0.3", Files: map[string]UpdateFile{
		"amd64": {URL: "https://x/exe", SHA256: sum, Size: 1},
	}}
	if err := SignUpdateManifest(m, priv); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(m)
	fetch := func(u string) ([]byte, error) {
		switch u {
		case "https://x/sums":
			return []byte(sum + "  WIN7Harness-amd64.exe\n"), nil
		case "https://x/latest":
			return data, nil
		}
		return nil, fmt.Errorf("unexpected %s", u)
	}
	if err := verifyGitHubManifest(rel, UpdateFile{SHA256: sum}, fetch); err != nil {
		t.Fatalf("合法签名应通过：%v", err)
	}
	if err := verifyGitHubManifest(rel, UpdateFile{SHA256: strings.Repeat("b", 64)}, fetch); err == nil {
		t.Fatal("清单与 SHA256SUMS 不一致时应拒绝")
	}
	if err := verifyGitHubManifest(rel, UpdateFile{SHA256: sum}, func(string) ([]byte, error) { return []byte("{}"), nil }); err == nil {
		t.Fatal("无签名清单应拒绝")
	}
}
