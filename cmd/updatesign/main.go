// updatesign 生成 Ed25519 更新签名密钥，或给 latest.json 签名。
//
// 生成：
//
//	go run ./cmd/updatesign -gen -dir keys
//	# keys/update_private.key 自己保管，绝不提交；keys/update_public.key 用于构建
//
// 签名：
//
//	go run ./cmd/updatesign -key keys/update_private.key -in latest.json
//
// 构建时注入公钥：
//
//	UPDATE_PUBKEY=$(cat keys/update_public.key) ./build.sh
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"win7harness/internal/core"
)

func main() {
	gen := flag.Bool("gen", false, "生成新的 Ed25519 密钥对")
	dir := flag.String("dir", ".", "生成密钥的目录")
	keyPath := flag.String("key", "", "私钥文件（base64）")
	inPath := flag.String("in", "", "待签名清单 JSON")
	outPath := flag.String("out", "", "输出路径（默认覆盖 -in）")
	force := flag.Bool("force", false, "覆盖已存在的密钥文件")
	flag.Parse()

	if *gen {
		generate(*dir, *force)
		return
	}
	if strings.TrimSpace(*keyPath) == "" || strings.TrimSpace(*inPath) == "" {
		fmt.Fprintln(os.Stderr, "用法：updatesign -key update_private.key -in latest.json [-out latest.json]")
		os.Exit(2)
	}
	sign(*keyPath, *inPath, *outPath)
}

func generate(dir string, force bool) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
	}
	privPath := filepath.Join(dir, "update_private.key")
	pubPath := filepath.Join(dir, "update_public.key")
	if !force {
		for _, p := range []string{privPath, pubPath} {
			if _, err := os.Stat(p); err == nil {
				fail(fmt.Errorf("%s 已存在；确认要覆盖请加 -force", p))
			}
		}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fail(err)
	}
	privB64 := base64.StdEncoding.EncodeToString(priv)
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	if err := os.WriteFile(privPath, []byte(privB64+"\n"), 0o600); err != nil {
		fail(err)
	}
	if err := os.WriteFile(pubPath, []byte(pubB64+"\n"), 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("已生成：\n  %s（私钥，务必离线备份，绝不提交）\n  %s（公钥）\n", privPath, pubPath)
	fmt.Printf("\n构建时注入公钥：\n  UPDATE_PUBKEY=%s ./build.sh\n", pubB64)
}

func sign(keyPath, inPath, outPath string) {
	keyRaw, err := os.ReadFile(keyPath)
	if err != nil {
		fail(err)
	}
	priv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyRaw)))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		fail(fmt.Errorf("%s 不是合法的 Ed25519 私钥（base64，64 字节）", keyPath))
	}
	body, err := os.ReadFile(inPath)
	if err != nil {
		fail(err)
	}
	m, err := core.ParseUpdateManifest(body)
	if err != nil {
		fail(fmt.Errorf("清单解析失败：%v", err))
	}
	if err := core.SignUpdateManifest(m, ed25519.PrivateKey(priv)); err != nil {
		fail(err)
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		fail(err)
	}
	if outPath == "" {
		outPath = inPath
	}
	if err := core.WriteFileAtomic(outPath, append(out, '\n'), 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("已签名 %s（signature 为 %d 字符 base64）\n", outPath, len(m.Signature))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "错误："+err.Error())
	os.Exit(1)
}
