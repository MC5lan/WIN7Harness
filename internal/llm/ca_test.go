package llm

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMergedCABundleHasCerts(t *testing.T) {
	b, err := MergedCABundle("")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("BEGIN CERTIFICATE")) {
		t.Fatal("合并 CA 包里没有证书")
	}
}

func TestExportCABundle(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ca.pem")
	if err := ExportCABundle(p, ""); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil || st.Size() == 0 {
		t.Fatalf("导出文件异常: %v size=%d", err, st.Size())
	}
}
