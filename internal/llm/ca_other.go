//go:build !windows

package llm

import "os"

func systemCertsPEM() []byte {
	for _, p := range []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/pki/tls/certs/ca-bundle.crt",
		"/etc/ssl/cert.pem",
	} {
		if b, err := os.ReadFile(p); err == nil {
			return b
		}
	}
	return nil
}
