//go:build windows

package win32

import (
	"bytes"
	"encoding/pem"
	"fmt"
	"syscall"
	"unsafe"
)

// 把 Windows 系统根证书导出成 PEM，供 Git/Node/Python/curl 等子进程使用。
//
// Win7 的根证书库可能很旧，但企业 MITM 代理的根证书通常已经由 IT 装进
// “受信任的根证书颁发机构”。Go 自己能用系统池，Node/Python/Git 却各有
// 各的 CA bundle；把系统 ROOT/CA 合并导出，子进程才能复用同一套信任链。

var (
	pCertOpenSystemStoreW        = dCrypt32.NewProc("CertOpenSystemStoreW")
	pCertEnumCertificatesInStore = dCrypt32.NewProc("CertEnumCertificatesInStore")
	pCertCloseStore              = dCrypt32.NewProc("CertCloseStore")
)

var dCrypt32 = syscall.NewLazyDLL("crypt32.dll")

type certContext struct {
	DwCertEncodingType uint32
	PbCertEncoded      *byte
	CbCertEncoded      uint32
	PCertInfo          unsafe.Pointer
	HCertStore         uintptr
}

// SystemRootsPEM 返回 ROOT + CA 证书库里的所有证书（PEM）。
// 单个证书损坏/存储打开失败不会中断整体导出。
func SystemRootsPEM() ([]byte, error) {
	var out bytes.Buffer
	okAny := false
	for _, name := range []string{"ROOT", "CA"} {
		namePtr, err := syscall.UTF16PtrFromString(name)
		if err != nil {
			continue
		}
		store, _, _ := pcall(pCertOpenSystemStoreW, 0, uintptr(unsafe.Pointer(namePtr)))
		if store == 0 {
			continue
		}
		okAny = true
		var ctx uintptr
		for {
			// CertEnumCertificatesInStore 会释放上一个 context；最后一个由下一次
			// 调用（返回 0）释放，调用方不要再 free，避免 double free。
			ctx, _, _ = pcall(pCertEnumCertificatesInStore, store, ctx)
			if ctx == 0 {
				break
			}
			cc := (*certContext)(unsafe.Pointer(ctx))
			if cc.PbCertEncoded == nil || cc.CbCertEncoded == 0 {
				continue
			}
			der := unsafe.Slice(cc.PbCertEncoded, cc.CbCertEncoded)
			_ = pem.Encode(&out, &pem.Block{Type: "CERTIFICATE", Bytes: der})
		}
		_, _, _ = pcall(pCertCloseStore, store, 0)
	}
	if !okAny {
		return nil, fmt.Errorf("无法打开 Windows 证书库")
	}
	return out.Bytes(), nil
}
