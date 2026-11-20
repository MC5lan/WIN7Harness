package core

import (
	"archive/zip"
	"io"
	"testing"
)

func zipNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("打开 zip 失败: %v", err)
	}
	defer zr.Close()
	out := map[string]bool{}
	for _, f := range zr.File {
		out[f.Name] = true
	}
	return out
}

func zipRead(t *testing.T, path, name string) string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("打开 zip 失败: %v", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return string(b)
	}
	t.Fatalf("zip 内无 %s", name)
	return ""
}
