package core

import "testing"

func TestIsUNCPath(t *testing.T) {
	yes := []string{`\\server\share`, `//server/share`, `\\?\UNC\server\share`, `\??\UNC\server\share`}
	for _, p := range yes {
		if !IsUNCPath(p) {
			t.Fatalf("%q 应识别为 UNC", p)
		}
	}
	no := []string{`C:\work\a.txt`, `\\?\C:\work\a.txt`, `\\.\COM1`, `relative\path`}
	for _, p := range no {
		if IsUNCPath(p) {
			t.Fatalf("%q 不应识别为 UNC", p)
		}
	}
}
