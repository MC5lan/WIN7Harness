package core

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// —— 更新检查 ——
//
// 只做三件事：读清单、比版本、下载并校验。**不自动替换正在运行的 exe**。
// 原因很实际：正在运行的 exe 被系统锁着，任何"自动升级"都得先退出自己、
// 再靠外部进程换文件，中途断电就剩一个坏包，用户下不来也回不去。
// 这里把新版本下载到 updates 目录并校验 SHA256，然后告诉用户路径，
// 由用户决定什么时候替换——比替他冒险划算。

// UpdateFile 一个可下载的构建产物。
type UpdateFile struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

// UpdateManifest 发布清单（放在任意 HTTP 可达位置，JSON）。
type UpdateManifest struct {
	Version     string                `json:"version"`
	Notes       string                `json:"notes,omitempty"`
	PageURL     string                `json:"page_url,omitempty"`
	PublishedAt string                `json:"published_at,omitempty"`
	Files       map[string]UpdateFile `json:"files"`
	// Signature 是 Ed25519 对 canonicalManifestPayload 的签名（base64）。
	// 发布构建会通过 -X 注入 core.UpdatePublicKey；校验失败直接拒绝更新。
	Signature string `json:"signature,omitempty"`
}

// UpdatePublicKey 是发布公钥（base64，32 字节 Ed25519）。
// 为空表示开发构建，跳过验签并写一条日志；正式构建应由 build.sh 注入。
var UpdatePublicKey string

var updateKeyWarnOnce sync.Once

// DefaultManifestURL 默认清单地址（可在配置里覆盖）。
const DefaultManifestURL = "https://raw.githubusercontent.com/win7harness/releases/main/latest.json"

// canonicalManifestPayload 只序列化被签名保护的字段：
// 版本、说明、页面、发布时间与各架构文件（URL/SHA256/大小）。
// 不包含 Signature 本身，避免自引用；json.Marshal 对 map key 排序，
// 因此发布端与客户端得到完全一致的字节。
func canonicalManifestPayload(m *UpdateManifest) ([]byte, error) {
	if m == nil {
		return nil, errors.New("更新清单为空")
	}
	type payload struct {
		Version     string                `json:"version"`
		Notes       string                `json:"notes,omitempty"`
		PageURL     string                `json:"page_url,omitempty"`
		PublishedAt string                `json:"published_at,omitempty"`
		Files       map[string]UpdateFile `json:"files"`
	}
	return json.Marshal(payload{
		Version:     m.Version,
		Notes:       m.Notes,
		PageURL:     m.PageURL,
		PublishedAt: m.PublishedAt,
		Files:       m.Files,
	})
}

// SignUpdateManifest 用发布私钥给清单签名。发布工具 cmd/updatesign 使用。
func SignUpdateManifest(m *UpdateManifest, priv ed25519.PrivateKey) error {
	if len(priv) != ed25519.PrivateKeySize {
		return errors.New("Ed25519 私钥长度不正确")
	}
	payload, err := canonicalManifestPayload(m)
	if err != nil {
		return err
	}
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload))
	return nil
}

// VerifyUpdateManifest 用公钥验证清单签名。
func VerifyUpdateManifest(m *UpdateManifest, pubKey string) error {
	pubKey = strings.TrimSpace(pubKey)
	if pubKey == "" {
		return errors.New("没有配置更新公钥")
	}
	if m == nil || strings.TrimSpace(m.Signature) == "" {
		return errors.New("更新清单没有 signature 字段")
	}
	pub, err := base64.StdEncoding.DecodeString(pubKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("更新公钥不是合法的 Ed25519 base64（应为 32 字节）")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(m.Signature))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("更新清单 signature 不是合法的 Ed25519 base64（应为 64 字节）")
	}
	payload, err := canonicalManifestPayload(m)
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), payload, sig) {
		return errors.New("更新清单签名不匹配（可能被篡改或清单已过期）")
	}
	return nil
}

// ParseUpdateManifest 解析清单 JSON；缺版本号视为坏清单。
func ParseUpdateManifest(b []byte) (*UpdateManifest, error) {
	var m UpdateManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("清单不是合法 JSON：%v", err)
	}
	m.Version = strings.TrimSpace(m.Version)
	if m.Version == "" {
		return nil, errors.New("清单缺少 version 字段")
	}
	if _, _, _, _, err := ParseVersion(m.Version); err != nil {
		return nil, fmt.Errorf("清单版本号不可解析：%q", m.Version)
	}
	return &m, nil
}

// ParseVersion 解析 x.y.z[-pre]，允许前缀 v。
func ParseVersion(s string) (int, int, int, string, error) {
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	if raw == "" {
		return 0, 0, 0, "", errors.New("版本号为空")
	}
	pre := ""
	if i := strings.IndexAny(raw, "-+"); i >= 0 {
		pre = raw[i+1:]
		raw = raw[:i]
	}
	parts := strings.Split(raw, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, 0, "", fmt.Errorf("版本号应形如 1.27.0，收到 %q", s)
	}
	nums := [3]int{}
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || v < 0 {
			return 0, 0, 0, "", fmt.Errorf("版本号含非数字段：%q", s)
		}
		nums[i] = v
	}
	return nums[0], nums[1], nums[2], pre, nil
}

// CompareVersion 比较两个版本号：a>b 返回 1，相等 0，a<b 返回 -1。
// 预发布版本（带 -beta 之类）排在正式版之前，与常见语义一致。
func CompareVersion(a, b string) (int, error) {
	a1, a2, a3, apre, err := ParseVersion(a)
	if err != nil {
		return 0, err
	}
	b1, b2, b3, bpre, err := ParseVersion(b)
	if err != nil {
		return 0, err
	}
	cmp := func(x, y int) int {
		switch {
		case x > y:
			return 1
		case x < y:
			return -1
		}
		return 0
	}
	if c := cmp(a1, b1); c != 0 {
		return c, nil
	}
	if c := cmp(a2, b2); c != 0 {
		return c, nil
	}
	if c := cmp(a3, b3); c != 0 {
		return c, nil
	}
	switch {
	case apre == bpre:
		return 0, nil
	case apre == "":
		return 1, nil // 正式版 > 预发布
	case bpre == "":
		return -1, nil
	default:
		return comparePrerelease(apre, bpre), nil
	}
}

// comparePrerelease 按 semver 预发布比较：按 "." 分段逐段比较，
// 纯数字段按数值比较（beta.10 > beta.2），含字母段按字典序；
// 数字段恒小于字母段（1 < alpha，见 semver.org §11.4.2）。
func comparePrerelease(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		ax, bx := as[i], bs[i]
		an, aerr := strconv.Atoi(ax)
		bn, berr := strconv.Atoi(bx)
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				if an > bn {
					return 1
				}
				return -1
			}
		case aerr == nil:
			return -1 // 数字段 < 字母段
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(ax, bx); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(as) > len(bs):
		return 1
	case len(as) < len(bs):
		return -1
	}
	return 0
}

// UpdateFetcher 取清单的函数，注入以便测试与复用代理/TLS 设置。
type UpdateFetcher func(url string) ([]byte, error)

// HTTPFetcher 用内置 http 客户端取清单；超时 15 秒，失败不重试
// （更新检查是后台小事，卡住主流程才叫问题）。
func HTTPFetcher(url string) ([]byte, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Win7Harness/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("清单请求失败：HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return body, nil
}

// CheckUpdate 拉清单并与当前版本比较，返回清单与"是否有更新"。
func CheckUpdate(currentVersion, manifestURL string, fetch UpdateFetcher) (*UpdateManifest, bool, error) {
	if fetch == nil {
		fetch = HTTPFetcher
	}
	if strings.TrimSpace(manifestURL) == "" {
		manifestURL = DefaultManifestURL
	}
	body, err := fetch(manifestURL)
	if err != nil {
		return nil, false, err
	}
	m, err := ParseUpdateManifest(body)
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(UpdatePublicKey) == "" {
		updateKeyWarnOnce.Do(func() {
			Logf("update: 未配置 UpdatePublicKey，跳过清单验签（仅开发构建；正式发布请注入公钥）")
		})
	} else if err := VerifyUpdateManifest(m, UpdatePublicKey); err != nil {
		return nil, false, fmt.Errorf("更新清单验签失败：%w", err)
	}
	cmp, err := CompareVersion(m.Version, currentVersion)
	if err != nil {
		return nil, false, err
	}
	return m, cmp > 0, nil
}

// FileFor 取对应架构的下载项。
func (m *UpdateManifest) FileFor(arch string) (UpdateFile, bool) {
	if m == nil || len(m.Files) == 0 {
		return UpdateFile{}, false
	}
	if f, ok := m.Files[arch]; ok && f.URL != "" {
		return f, true
	}
	// 用户机器是 386 但发布只提供 amd64 时，不要"善意"降级下载——
	// 装上去会直接启动失败，返回 false 让上层说清楚，比错装好。
	return UpdateFile{}, false
}

// UpdatesDir 更新包下载目录。
func UpdatesDir() string { return filepath.Join(LocalDataDir(), "updates") }

// DownloadUpdate 下载并校验一个更新包，返回本地绝对路径。
// 校验失败会删掉半个文件，避免用户拿一个坏 exe 去覆盖正在用的版本。
func DownloadUpdate(f UpdateFile, arch string, fetch UpdateFetcher) (string, error) {
	if f.URL == "" {
		return "", errors.New("清单里没有这个架构的下载地址")
	}
	if fetch == nil {
		fetch = func(u string) ([]byte, error) {
			client := &http.Client{Timeout: 5 * time.Minute}
			req, err := http.NewRequest("GET", u, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Set("User-Agent", "Win7Harness/"+Version)
			resp, err := client.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				return nil, fmt.Errorf("下载失败：HTTP %d", resp.StatusCode)
			}
			// 多读 1 字节：刚好 200MB 是合法包，读到 200MB+1 就说明被截断/包超限，
			// 不能把半拉条 exe 当好包装上去。
			const maxSize = 200 << 20
			b, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
			if err != nil {
				return nil, err
			}
			if len(b) > maxSize {
				return nil, errors.New("更新包超过 200MB，已拒绝（文件过大或清单有误）")
			}
			return b, nil
		}
	}
	data, err := fetch(f.URL)
	if err != nil {
		return "", err
	}
	if len(data) < 1024 {
		return "", fmt.Errorf("下载内容过小（%d 字节），可能不是可执行文件", len(data))
	}
	if f.Size > 0 && int64(len(data)) != f.Size {
		return "", fmt.Errorf("更新包大小不符：清单 %d 字节，实际 %d 字节", f.Size, len(data))
	}
	// SHA256 是唯一可靠的完整性防线（架构头校验兜不住“半拉条 PE”）。
	// 清单没写 sha256：fail-closed，拒绝安装——此前是静默跳过校验。
	want := strings.ToLower(strings.TrimSpace(f.SHA256))
	if want == "" {
		return "", errors.New("更新清单没有提供 SHA256，拒绝安装（无法验证完整性）；发布方需要为每个文件生成 SHA256 并写入清单")
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return "", fmt.Errorf("SHA256 校验失败：期望 %s，实际 %s", want[:12]+"…", got[:12]+"…")
	}
	dir := UpdatesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("Win7Harness-%s-%s.exe", arch, time.Now().Format("20060102-150405"))
	p := filepath.Join(dir, name)
	tmp := p + ".part"
	if err := WriteFileAtomic(tmp, data, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return p, nil
}

// ShortNotes 返回清单说明的首行摘要（最多 120 字），供提示条使用。
func (m *UpdateManifest) ShortNotes() string {
	if m == nil {
		return ""
	}
	notes := strings.TrimSpace(m.Notes)
	if notes == "" {
		return ""
	}
	line := strings.SplitN(notes, "\n", 2)[0]
	r := []rune(strings.TrimSpace(line))
	if len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return string(r)
}
