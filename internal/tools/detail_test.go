package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileReturnsUIDiff(t *testing.T) {
	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
	dir := t.TempDir()
	p := filepath.Join(dir, "demo.txt")
	if err := os.WriteFile(p, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{WorkDir: dir}
	res, err := runEditFile(ctx, map[string]interface{}{
		"path": "demo.txt", "old_string": "beta", "new_string": "BETA",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Detail == "" {
		t.Fatal("edit_file 未返回界面 diff")
	}
	if !strings.Contains(res.Detail, "-beta") || !strings.Contains(res.Detail, "+BETA") {
		t.Fatalf("diff 内容异常：%s", res.Detail)
	}
	if strings.Contains(res.Output, "-beta") {
		t.Fatal("diff 不应混进模型可见的 Output")
	}
	// 确认写盘仍然成功。
	got, _ := os.ReadFile(p)
	if !strings.Contains(string(got), "BETA") {
		t.Fatal("文件未写入")
	}
}
