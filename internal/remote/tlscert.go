package remote

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"win7harness/internal/core"
)

// CertStorePath 证书与私钥的持久化位置（数据目录下，0600）。
func CertStorePath() string {
	return filepath.Join(core.LocalDataDir(), "remote-cert.pem")
}

// LoadOrCreateSelfSignedCert 返回可用证书：存在且 30 天后才过期就复用，
// 否则重新生成并落盘 0600。
//
// 此前每次启动现签一张——指纹每次都变，手机浏览器每次都拦、都得点"继续"，
// 用户对指纹警告麻木 == 自签 HTTPS 的全部安全价值归零。
func LoadOrCreateSelfSignedCert(hosts []string, days int) (certPEM, keyPEM []byte, err error) {
	p := CertStorePath()
	if certPEM, keyPEM, ok := readCertIfFresh(p); ok {
		return certPEM, keyPEM, nil
	}
	certPEM, keyPEM, err = SelfSignedCert(hosts, days)
	if err != nil {
		return nil, nil, err
	}
	if werr := writeFileAtomic0600(p, append(append(append(certPEM, '\n'), keyPEM...))); werr != nil {
		// 落盘失败不致命：本次用内存里的证书跑，下次再试（但日志要能看出来）
		core.Logf("remote: 自签证书落盘失败（%s）：%v", p, werr)
	}
	return certPEM, keyPEM, nil
}

// readCertIfFresh 读落盘证书：能解析、证书与私钥匹配、30 天以上有效期 → 复用。
func readCertIfFresh(p string) (certPEM, keyPEM []byte, ok bool) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, nil, false
	}
	var certDER, keyDER []byte
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch block.Type {
		case "CERTIFICATE":
			certDER = block.Bytes
		case "RSA PRIVATE KEY":
			keyDER = block.Bytes
		}
	}
	if certDER == nil || keyDER == nil {
		return nil, nil, false
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, false
	}
	if time.Until(cert.NotAfter) < 30*24*time.Hour {
		return nil, nil, false
	}
	key, err := x509.ParsePKCS1PrivateKey(keyDER)
	if err != nil {
		return nil, nil, false
	}
	// 证书与私钥必须匹配（防止落盘的是一对不匹配的文件）
	pub, ok := key.Public().(*rsa.PublicKey)
	if !ok {
		return nil, nil, false
	}
	cpub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, nil, false
	}
	if pub.N.Cmp(cpub.N) != 0 {
		return nil, nil, false
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER}), true
}

// writeFileAtomic0600 原子写 0600 文件。
func writeFileAtomic0600(path string, data []byte) error {
	tmp := path + ".tmp." + time.Now().Format("150405.000000")
	if err := core.WriteFileAtomic(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// SelfSignedCert 生成一张局域网用的自签证书。
//
// 用 2048 位 RSA 而不是 ECC：Win7/XP 时代的系统与部分安卓浏览器
// 对 ECDSA 证书支持参差，RSA 最稳。这张证书不是为了取信浏览器
// （自签一定不被信任），只是为了让链路加密，所以不上 CA、不做吊销。
func SelfSignedCert(hosts []string, days int) (certPEM, keyPEM []byte, err error) {
	if days <= 0 {
		days = 365
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("生成私钥失败：%v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("生成序列号失败：%v", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Win7 Harness", Organization: []string{"Win7 Harness"}},
		NotBefore:             now.Add(-time.Hour), // 容忍手机与电脑的时钟误差
		NotAfter:              now.AddDate(0, 0, days),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	seen := map[string]bool{}
	for _, h := range hosts {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			continue
		}
		tmpl.DNSNames = append(tmpl.DNSNames, h)
	}
	tmpl.DNSNames = append(tmpl.DNSNames, "localhost")
	if len(tmpl.IPAddresses) == 0 {
		tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP("127.0.0.1"))
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("签发证书失败：%v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		return nil, nil, errors.New("证书编码失败")
	}
	return certPEM, keyPEM, nil
}

// CertDays 自签证书有效期。一年足够：到期时手机只会再点一次「继续」，
// 而短有效期会让用户每隔几周就怀疑是不是坏了。
const CertDays = 365

// CertHosts 证书里要覆盖的主机名：本机名 + 所有局域网 IP + 回环。
// 少一个都可能让手机浏览器直接拒绝连接，所以宁可多写几个。
func CertHosts() []string {
	hosts := []string{"localhost", "127.0.0.1"}
	if name, err := os.Hostname(); err == nil && strings.TrimSpace(name) != "" {
		hosts = append(hosts, name)
	}
	for _, ip := range LocalIPs() {
		hosts = append(hosts, ip)
	}
	return hosts
}
