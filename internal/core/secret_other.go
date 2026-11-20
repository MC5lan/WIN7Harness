//go:build !windows

package core

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// 非 Windows 只用于开发机单测；这不是安全存储，只是让配置读写能往返。
const secretPrefix = "dev:v1:"

func sealSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	return secretPrefix + base64.StdEncoding.EncodeToString([]byte(plain)), nil
}

func openSecret(enc string) (string, error) {
	enc = strings.TrimSpace(enc)
	if !strings.HasPrefix(enc, secretPrefix) {
		return "", fmt.Errorf("开发机只支持 dev:v1: 密文")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(enc, secretPrefix))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
