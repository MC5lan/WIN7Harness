package llm

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"fmt"
	"os"
	"strings"
)

// 内置公共根证书兜底（22 张，均取自 Mozilla CA 集合且当前有效）。
// Win7 已停止更新根证书，缺 KB4474419 或根证书过期时会报
// "x509: certificate signed by unknown authority"，此时用系统池 + 内置池合并校验。
//
//go:embed ca_bundle.pem
var bundledRoots []byte

// TLSOptions TLS 相关设置。
type TLSOptions struct {
	// ExtraCAFile 用户提供的补充根证书（PEM）。企业代理/自签网关场景用。
	ExtraCAFile string
	// Insecure 跳过证书校验。仅排查问题用，默认关闭。
	Insecure bool
}

// NewTLSConfig 构建客户端 TLS 配置：系统根证书池 + 内置兜底 + 用户补充证书。
func NewTLSConfig(opt TLSOptions) (*tls.Config, error) {
	if opt.Insecure {
		return &tls.Config{
			InsecureSkipVerify: true,
			MinVersion:         tls.VersionTLS12,
		}, nil
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	// 合并而不是替换：系统池里的新根也要保留。
	if n := addPEM(pool, bundledRoots); n == 0 {
		// 内置池理论上不会失败，真失败时至少还能靠系统池。
		_ = n
	}

	if f := strings.TrimSpace(opt.ExtraCAFile); f != "" {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("读取补充根证书失败：%v", err)
		}
		if addPEM(pool, data) == 0 {
			return nil, fmt.Errorf("补充根证书文件中没有可用证书（需 PEM 格式）")
		}
	}

	return &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}, nil
}

// addPEM 把 PEM 里的证书加入池子，返回加入数量。
func addPEM(pool *x509.CertPool, pemData []byte) int {
	if pool == nil || len(pemData) == 0 {
		return 0
	}
	n := 0
	rest := pemData
	for {
		var block []byte
		block, rest = nextCertBlock(rest)
		if block == nil {
			break
		}
		if pool.AppendCertsFromPEM(block) {
			n++
		}
	}
	return n
}

// nextCertBlock 从数据里截出下一个 PEM 证书块，返回剩余部分。
func nextCertBlock(data []byte) (block, rest []byte) {
	const begin = "-----BEGIN CERTIFICATE-----"
	const end = "-----END CERTIFICATE-----"
	s := string(data)
	i := strings.Index(s, begin)
	if i < 0 {
		return nil, nil
	}
	s = s[i:]
	j := strings.Index(s, end)
	if j < 0 {
		return nil, nil
	}
	j += len(end)
	// 保留块尾换行，AppendCertsFromPEM 不介意，但更稳妥。
	if j < len(s) && s[j] == '\n' {
		j++
	}
	return []byte(s[:j]), []byte(s[j:])
}
