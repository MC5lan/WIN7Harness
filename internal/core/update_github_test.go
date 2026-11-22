//go:build windows

package core

import (
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
