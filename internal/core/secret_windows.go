//go:build windows

package core

import (
	"encoding/base64"
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

// API Key 落盘前用 DPAPI（CryptProtectData）按当前 Windows 用户加密。
//
// 这样即使 config.json 被同步盘、备份工具、截图或误发给别人，拿到的也只是
// 一段只在本机/本用户下能解开的密文。拷到另一台机器或另一个用户下时解密失败，
// 程序会清空内存里的 Key 并提示重新填写，而不是拿着密文去请求 API。
//
// 加密格式：dpapi:v1:<base64(DATA_BLOB)>
// 前缀用于以后换算法/换 scope，同时让旧版明文 api_key 仍可被识别。

const secretPrefix = "dpapi:v1:"
const cryptProtectUIForbidden = 0x1

var (
	dCrypt32            = syscall.NewLazyDLL("crypt32.dll")
	pCryptProtectData   = dCrypt32.NewProc("CryptProtectData")
	pCryptUnprotectData = dCrypt32.NewProc("CryptUnprotectData")
	pLocalFree          = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	CbData uint32
	PbData *byte
}

func sealSecret(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if err := pCryptProtectData.Find(); err != nil {
		return "", fmt.Errorf("当前系统没有 DPAPI：%v", err)
	}
	in := []byte(plain)
	inBlob := dataBlob{CbData: uint32(len(in)), PbData: &in[0]}
	var out dataBlob
	r, _, err := pCryptProtectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0, // description
		0, // optional entropy
		0, // reserved
		0, // prompt
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return "", fmt.Errorf("DPAPI 加密失败：%v", err)
	}
	defer pLocalFree.Call(uintptr(unsafe.Pointer(out.PbData)))
	blob := unsafe.Slice(out.PbData, out.CbData)
	return secretPrefix + base64.StdEncoding.EncodeToString(blob), nil
}

func openSecret(enc string) (string, error) {
	enc = strings.TrimSpace(enc)
	if !strings.HasPrefix(enc, secretPrefix) {
		return "", fmt.Errorf("未知的密钥密文格式")
	}
	if err := pCryptUnprotectData.Find(); err != nil {
		return "", fmt.Errorf("当前系统没有 DPAPI：%v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(enc, secretPrefix))
	if err != nil || len(raw) == 0 {
		return "", fmt.Errorf("密钥密文不是合法 base64")
	}
	inBlob := dataBlob{CbData: uint32(len(raw)), PbData: &raw[0]}
	var out dataBlob
	r, _, derr := pCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0, 0, 0, 0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return "", fmt.Errorf("DPAPI 解密失败（配置可能来自其他 Windows 用户/机器）：%v", derr)
	}
	defer pLocalFree.Call(uintptr(unsafe.Pointer(out.PbData)))
	return string(unsafe.Slice(out.PbData, out.CbData)), nil
}
