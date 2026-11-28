package lsp

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type captureWriteCloser struct{ buf bytes.Buffer }

func (c *captureWriteCloser) Write(p []byte) (int, error) { return c.buf.Write(p) }
func (c *captureWriteCloser) Close() error               { return nil }

// 客户端复用后，同一文件第二次必须发 didChange，不能重复 didOpen。
func TestDidOpenReusesDocument(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	if err := os.WriteFile(p, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &captureWriteCloser{}
	c := &Client{in: w, open: map[string]int{}}

	if _, err := c.DidOpen(p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.buf.String(), "textDocument/didOpen") {
		t.Fatalf("首次应发 didOpen：%s", w.buf.String())
	}
	w.buf.Reset()
	if err := os.WriteFile(p, []byte("package a\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DidOpen(p); err != nil {
		t.Fatal(err)
	}
	got := w.buf.String()
	if !strings.Contains(got, "textDocument/didChange") || strings.Contains(got, "textDocument/didOpen") {
		t.Fatalf("第二次应发 didChange：%s", got)
	}
	if !strings.Contains(got, `"version":2`) {
		t.Fatalf("didChange 版本应为 2：%s", got)
	}
}

var _ io.WriteCloser = (*captureWriteCloser)(nil)
