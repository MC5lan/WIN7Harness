// make_manifest 从 dist/ 构建产物生成并签名 latest.json。
//
// 用法：
//
//	UPDATE_PRIVATE_KEY=<base64> go run ./tools/make_manifest \
//	  -version 1.38.2 -dist dist -out dist/latest.json
//
// 私钥只从环境变量读取（-key-from-env 指定变量名），不落盘、不出现在命令行，
// 避免进程列表与构建日志泄漏。签名必须走 core.SignUpdateManifest，保证与
// 客户端 core.VerifyUpdateManifest 使用完全相同的 canonical payload。
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"win7harness/internal/core"
)

func main() {
	version := flag.String("version", "", "版本号（可带 v 前缀）")
	dist := flag.String("dist", "dist", "构建产物目录")
	out := flag.String("out", "dist/latest.json", "输出清单路径")
	keyEnv := flag.String("key-from-env", "UPDATE_PRIVATE_KEY", "存放 base64 私钥的环境变量名")
	notesPath := flag.String("notes", "", "发布说明文件（可选）")
	pageURL := flag.String("page-url", "", "发布页面 URL（默认指向对应 tag）")
	repo := flag.String("repo", "MC5lan/WIN7Harness", "GitHub 仓库 owner/name")
	flag.Parse()

	ver := strings.TrimPrefix(strings.TrimSpace(*version), "v")
	if ver == "" {
		fail("缺少 -version")
	}
	if _, _, _, _, err := core.ParseVersion(ver); err != nil {
		fail(fmt.Sprintf("版本号不可解析：%v", err))
	}

	page := strings.TrimSpace(*pageURL)
	if page == "" {
		page = fmt.Sprintf("https://github.com/%s/releases/tag/v%s", strings.Trim(*repo, "/"), ver)
	}

	notes := ""
	if strings.TrimSpace(*notesPath) != "" {
		b, err := os.ReadFile(*notesPath)
		if err != nil {
			fail(fmt.Sprintf("读取发布说明失败：%v", err))
		}
		notes = strings.TrimSpace(string(b))
	}

	m := &core.UpdateManifest{
		Version:     ver,
		Notes:       notes,
		PageURL:     page,
		PublishedAt: time.Now().UTC().Format(time.RFC3339),
		Files:       map[string]core.UpdateFile{},
	}

	for _, arch := range []string{"amd64", "386"} {
		name := "WIN7Harness-" + arch + ".exe"
		path := filepath.Join(*dist, name)
		f, err := os.Open(path)
		if err != nil {
			fail(fmt.Sprintf("缺少 %s：%v", path, err))
		}
		h := sha256.New()
		size, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			fail(fmt.Sprintf("读取 %s 失败：%v", path, err))
		}
		if size == 0 {
			fail(fmt.Sprintf("%s 是空文件", path))
		}
		m.Files[arch] = core.UpdateFile{
			URL:    fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", strings.Trim(*repo, "/"), ver, name),
			SHA256: hex.EncodeToString(h.Sum(nil)),
			Size:   size,
		}
	}

	keyB64 := strings.TrimSpace(os.Getenv(*keyEnv))
	if keyB64 == "" {
		fail(fmt.Sprintf("环境变量 %s 为空（私钥只允许通过环境变量传入）", *keyEnv))
	}
	priv, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		fail(fmt.Sprintf("环境变量 %s 不是合法的 Ed25519 base64 私钥（应为 64 字节）", *keyEnv))
	}
	if err := core.SignUpdateManifest(m, ed25519.PrivateKey(priv)); err != nil {
		fail(fmt.Sprintf("签名失败：%v", err))
	}

	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fail(fmt.Sprintf("序列化清单失败：%v", err))
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fail(fmt.Sprintf("创建输出目录失败：%v", err))
	}
	if err := core.WriteFileAtomic(*out, body, 0o644); err != nil {
		fail(fmt.Sprintf("写入清单失败：%v", err))
	}

	fmt.Printf("已生成 %s\n  version=%s files=%d signature=%d chars\n", *out, ver, len(m.Files), len(m.Signature))
	for _, arch := range []string{"amd64", "386"} {
		fmt.Printf("  %-5s %d bytes  sha256=%s\n", arch, m.Files[arch].Size, m.Files[arch].SHA256)
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "错误："+msg)
	os.Exit(1)
}
