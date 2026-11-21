package core

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestSpillAndPruneArtifacts(t *testing.T) {
	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
	p, err := SpillArtifact(`C:\work\demo`, "run_tests", strings.Repeat("失败详情\n", 100))
	if err != nil {
		t.Fatal(err)
	}
	if p == "" {
		t.Fatal("未生成 artifact 路径")
	}
	data, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(data), "失败详情") {
		t.Fatalf("artifact 内容异常：%v", err)
	}
	// 造一个明显过期的文件，确认清理会删掉它。
	old := p + ".old"
	if err := os.WriteFile(old, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(old, oldTime, oldTime)
	PruneArtifacts(time.Hour, 100)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("过期 artifact 未被清理")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("新 artifact 不应被清理")
	}
}
