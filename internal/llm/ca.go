package llm

import (
	"bytes"
	"crypto/sha256"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"win7harness/internal/core"
)

// MergedCABundle 合并：当前系统根证书 + 内置 Mozilla 兜底 + 用户补充证书。
// 子进程（git/node/python/curl）各有各的 CA 文件，导出同一份能避免
// “浏览器能访问、git 报 unknown authority”这类企业代理问题。
func MergedCABundle(extraFile string) ([]byte, error) {
	var out bytes.Buffer
	seen := map[[32]byte]bool{}
	add := func(data []byte) {
		for {
			block, rest := pem.Decode(data)
			if block == nil {
				return
			}
			data = rest
			if block.Type != "CERTIFICATE" {
				continue
			}
			h := sha256.Sum256(block.Bytes)
			if seen[h] {
				continue
			}
			seen[h] = true
			_ = pem.Encode(&out, block)
		}
	}
	add(systemCertsPEM())
	add(bundledRoots)
	if f := trimSpace(extraFile); f != "" {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("读取补充根证书失败：%v", err)
		}
		add(data)
	}
	if out.Len() == 0 {
		return nil, fmt.Errorf("没有可用根证书")
	}
	return out.Bytes(), nil
}

// ExportCABundle 把合并后的 CA 写到 path，供子进程环境变量引用。
func ExportCABundle(path, extraFile string) error {
	data, err := MergedCABundle(extraFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return core.WriteFileAtomic(path, data, 0o644)
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r' || s[0] == '\n') {
		s = s[1:]
	}
	for len(s) > 0 {
		c := s[len(s)-1]
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}
