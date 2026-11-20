//go:build windows

package llm

import "win7harness/internal/win32"

func systemCertsPEM() []byte {
	b, err := win32.SystemRootsPEM()
	if err != nil {
		return nil
	}
	return b
}
