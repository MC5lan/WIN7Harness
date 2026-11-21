package tools

import (
	"strings"
	"testing"

	"win7harness/internal/core"
)

func TestReadArtifactRestrictedAndPaged(t *testing.T) {
	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
	p, err := core.SpillArtifact(t.TempDir(), "run_tests", "第一行\n第二行\n第三行\n")
	if err != nil {
		t.Fatal(err)
	}
	res, err := runReadArtifact(&Context{WorkDir: t.TempDir()}, map[string]interface{}{
		"path": p, "offset": 2, "limit": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "第二行") || !strings.Contains(res.Output, "第三行") || strings.Contains(res.Output, "第一行") {
		t.Fatalf("分段读取异常：%s", res.Output)
	}
	if _, err := runReadArtifact(&Context{WorkDir: t.TempDir()}, map[string]interface{}{
		"path": `C:\Windows\win.ini`,
	}); err == nil {
		t.Fatal("应拒绝 artifacts 目录之外的路径")
	}
}
