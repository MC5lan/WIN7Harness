package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// 便携模式的 API Key 保护。
//
// 标准模式用 Windows DPAPI，密文只在本机/本用户下能解开；U 盘要跨电脑用，
// 所以便携模式改为“用户设置的 U 盘密码”派生密钥，再用 AES-256-GCM 加密。
// 密码本身绝不落盘，每次启动由用户输入；忘了密码就只能清空密文重填 Key。
//
// 格式：pwd:v1:<迭代次数>:<salt-b64>:<nonce-b64>:<ciphertext-b64>
// 带版本前缀是为了以后换 KDF/算法时还能识别旧数据。

const (
	portableSecretPrefix = "pwd:v1:"
	portableSecretIter   = 150000
	portableSecretSalt   = 16
	portableSecretKeyLen = 32
	portableSecretNonce  = 12
)

var (
	// ErrPortablePasswordRequired 表示便携模式下有 Key 要落盘，但用户还没设置 U 盘密码。
	ErrPortablePasswordRequired = errors.New("便携模式：请先设置 U 盘密码，再保存 API Key")
	// ErrPortablePassword 表示密码错误，或密文被篡改。
	ErrPortablePassword = errors.New("U 盘密码错误，或密钥数据已损坏")
)

// sealPortableSecrets 用密码加密一段 JSON 明文，返回可放进 config.json 的字符串。
func sealPortableSecrets(plain []byte, password string) (string, error) {
	if password == "" {
		return "", ErrPortablePasswordRequired
	}
	salt := make([]byte, portableSecretSalt)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成 U 盘密钥盐失败：%w", err)
	}
	nonce := make([]byte, portableSecretNonce)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成 U 盘密钥随机数失败：%w", err)
	}
	key := pbkdf2SHA256([]byte(password), salt, portableSecretIter, portableSecretKeyLen)
	defer wipeBytes(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("初始化加密器失败：%w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("初始化 GCM 失败：%w", err)
	}
	ct := gcm.Seal(nil, nonce, plain, nil)
	return portableSecretPrefix +
		strconv.Itoa(portableSecretIter) + ":" +
		base64.StdEncoding.EncodeToString(salt) + ":" +
		base64.StdEncoding.EncodeToString(nonce) + ":" +
		base64.StdEncoding.EncodeToString(ct), nil
}

// openPortableSecrets 解开 sealPortableSecrets 的密文。
func openPortableSecrets(enc, password string) ([]byte, error) {
	enc = strings.TrimSpace(enc)
	if !strings.HasPrefix(enc, portableSecretPrefix) {
		return nil, fmt.Errorf("未知的便携密钥格式")
	}
	parts := strings.Split(strings.TrimPrefix(enc, portableSecretPrefix), ":")
	if len(parts) != 4 {
		return nil, fmt.Errorf("便携密钥格式不完整")
	}
	iter, err := strconv.Atoi(parts[0])
	if err != nil || iter < 1000 || iter > 2000000 {
		return nil, fmt.Errorf("便携密钥迭代次数不合法")
	}
	salt, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return nil, fmt.Errorf("便携密钥盐不合法")
	}
	nonce, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil || len(nonce) != portableSecretNonce {
		return nil, fmt.Errorf("便携密钥随机数不合法")
	}
	ct, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil || len(ct) < 16 {
		return nil, fmt.Errorf("便携密钥密文不合法")
	}
	key := pbkdf2SHA256([]byte(password), salt, iter, portableSecretKeyLen)
	defer wipeBytes(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化解密器失败：%w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 GCM 失败：%w", err)
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, ErrPortablePassword
	}
	return plain, nil
}

// pbkdf2SHA256 是 RFC 8018 的 PBKDF2-HMAC-SHA256。
// 只用标准库，不引入 x/crypto，避免在 Win7/Go 1.20 上被依赖版本卡住。
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	if iter < 1 {
		iter = 1
	}
	hLen := sha256.Size
	blocks := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, blocks*hLen)
	var counter [4]byte
	for block := 1; block <= blocks; block++ {
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		binary.BigEndian.PutUint32(counter[:], uint32(block))
		mac.Write(counter[:])
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iter; i++ {
			mac.Reset()
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

func wipeBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
