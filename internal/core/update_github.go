//go:build windows

package core

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// GitHub Releases 更新源（v1.40.2）。
//
// 安全模型（用户选型）：GitHub API 负责发现版本与资产，SHA256SUMS.txt 负责
// 完整性校验。没有提供校验和时 fail-closed，绝不把来路不明的 exe 标成“更新包”。
// 签名清单模式（UpdateSource=manifest）仍然保留，给正式发布使用。

// RuntimeArch 返回 386 / amd64，与 Release 资产命名一致。
func RuntimeArch() string {
	if Is386() {
		return "386"
	}
	return "amd64"
}

const (
	UpdateSourceGitHub   = "github"
	UpdateSourceManifest = "manifest"
	DefaultUpdateRepo    = "MC5lan/WIN7Harness"
)

// GitHubRelease 只取更新需要的字段，忽略 API 的其它大段 JSON。
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// validRepo 限制成 owner/repo，避免把用户填的字符串拼成任意 URL（SSRF）。
func validRepo(repo string) bool {
	parts := strings.Split(strings.TrimSpace(repo), "/")
	if len(parts) != 2 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 100 {
			return false
		}
		for _, r := range p {
			if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') &&
				!(r >= '0' && r <= '9') && r != '_' && r != '-' && r != '.' {
				return false
			}
		}
	}
	return true
}

// GitHubLatestURL 返回某个仓库最新 Release 的 API 地址。
func GitHubLatestURL(repo string) (string, error) {
	if !validRepo(repo) {
		return "", fmt.Errorf("GitHub 仓库格式应为 owner/repo，收到 %q", repo)
	}
	return "https://api.github.com/repos/" + strings.TrimSpace(repo) + "/releases/latest", nil
}

// FetchGitHubLatest 读取最新 Release 元数据。fetch 可注入以复用代理/TLS 设置。
func FetchGitHubLatest(repo string, fetch UpdateFetcher) (*GitHubRelease, error) {
	url, err := GitHubLatestURL(repo)
	if err != nil {
		return nil, err
	}
	if fetch == nil {
		fetch = HTTPFetcher
	}
	body, err := fetch(url)
	if err != nil {
		return nil, err
	}
	var rel GitHubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("解析 GitHub Release 失败：%w", err)
	}
	if rel.TagName == "" {
		return nil, errors.New("GitHub Release 没有 tag_name")
	}
	return &rel, nil
}

// normalizeTag 把 v1.40.2 / 1.40.2 都变成 1.40.2，便于与 core.Version 比较。
func normalizeTag(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}

// IsNewerGitHubRelease 比较 Release tag 与当前版本。
func IsNewerGitHubRelease(current, tag string) bool {
	cmp, err := CompareVersion(normalizeTag(tag), current)
	if err != nil {
		// 版本格式异常时不猜：字符串不同才提示，避免误报“已是最新”。
		return normalizeTag(tag) != "" && normalizeTag(tag) != current
	}
	return cmp > 0
}

// githubSums 解析 SHA256SUMS.txt：每行 "<64 hex>  <filename>"。
func githubSums(data []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || len(fields[0]) != 64 {
			continue
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		out[strings.ToLower(name)] = strings.ToLower(fields[0])
	}
	return out
}

// GitHubUpdateFile 从 Release 资产里挑出当前架构的 exe，并从 SHA256SUMS.txt
// 取出校验和。找不到校验和就报错——自动下载绝不在无完整性校验的情况下发生。
func GitHubUpdateFile(rel *GitHubRelease, arch string, fetch UpdateFetcher) (UpdateFile, error) {
	if rel == nil {
		return UpdateFile{}, errors.New("Release 为空")
	}
	want := strings.ToLower("WIN7Harness-" + arch + ".exe")
	var asset *struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	}
	var sumsAsset *struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	}
	for i := range rel.Assets {
		a := &rel.Assets[i]
		n := strings.ToLower(a.Name)
		if n == want {
			asset = a
		}
		if n == "sha256sums.txt" {
			sumsAsset = a
		}
	}
	if asset == nil || asset.BrowserDownloadURL == "" {
		return UpdateFile{}, fmt.Errorf("Release 里没有 %s 资产", want)
	}
	if sumsAsset == nil || sumsAsset.BrowserDownloadURL == "" {
		return UpdateFile{}, errors.New("Release 里没有 SHA256SUMS.txt，拒绝自动下载（无法校验完整性）")
	}
	if fetch == nil {
		fetch = HTTPFetcher
	}
	data, err := fetch(sumsAsset.BrowserDownloadURL)
	if err != nil {
		return UpdateFile{}, fmt.Errorf("读取 SHA256SUMS.txt 失败：%w", err)
	}
	sum := githubSums(data)[want]
	if sum == "" {
		return UpdateFile{}, fmt.Errorf("SHA256SUMS.txt 里没有 %s 的校验和", want)
	}
	return UpdateFile{URL: asset.BrowserDownloadURL, SHA256: sum, Size: asset.Size}, nil
}

// GitHubReleaseNotes 给界面用的一句话说明。
func GitHubReleaseNotes(rel *GitHubRelease) string {
	if rel == nil {
		return ""
	}
	if rel.Body != "" {
		return rel.Body
	}
	return rel.Name
}

// UpdateCheck 是两种更新源共用的检查结果，界面只认这一种结构。
type UpdateCheck struct {
	Version string
	Notes   string
	PageURL string
	File    UpdateFile
	Source  string
}

// CheckUpdateAny 按配置选择 GitHub Releases 或签名清单。
// GitHub 模式强制要求 SHA256SUMS.txt；清单模式沿用 Ed25519 验签。
func CheckUpdateAny(current, source, repo, manifestURL string, fetch UpdateFetcher) (*UpdateCheck, bool, error) {
	if fetch == nil {
		fetch = HTTPFetcher
	}
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "" {
		source = UpdateSourceGitHub
	}
	if source == UpdateSourceManifest {
		m, newer, err := CheckUpdate(current, manifestURL, fetch)
		if err != nil || !newer {
			return nil, false, err
		}
		f, ok := m.Files[RuntimeArch()]
		if !ok {
			return nil, false, fmt.Errorf("更新清单缺少 %s 架构", RuntimeArch())
		}
		return &UpdateCheck{Version: m.Version, Notes: m.ShortNotes(), PageURL: m.PageURL, File: f, Source: source}, true, nil
	}
	if source != UpdateSourceGitHub {
		return nil, false, fmt.Errorf("未知更新源 %q", source)
	}
	rel, err := FetchGitHubLatest(repo, fetch)
	if err != nil {
		return nil, false, err
	}
	if !IsNewerGitHubRelease(current, rel.TagName) {
		return nil, false, nil
	}
	f, err := GitHubUpdateFile(rel, RuntimeArch(), fetch)
	if err != nil {
		return nil, false, err
	}
	return &UpdateCheck{
		Version: normalizeTag(rel.TagName),
		Notes:   GitHubReleaseNotes(rel),
		PageURL: fmt.Sprintf("https://github.com/%s/releases/tag/%s", strings.TrimSpace(repo), rel.TagName),
		File:    f,
		Source:  source,
	}, true, nil
}
