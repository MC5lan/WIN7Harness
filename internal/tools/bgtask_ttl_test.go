package tools

import (
	"strings"
	"testing"
	"time"
)

// 后台任务不传 timeout 时应使用 20 分钟默认寿命，而不是前台的 120 秒。
func TestBackgroundDefaultTTL(t *testing.T) {
	res, err := runCommand(&Context{WorkDir: t.TempDir()}, map[string]interface{}{
		"command":    "cmd /c exit 0",
		"background": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "20m0s") {
		t.Fatalf("默认后台寿命不是 20 分钟：%q", res.Output)
	}
}

// 显式传 timeout 时，后台寿命按用户的秒数走。
func TestBackgroundExplicitTTL(t *testing.T) {
	res, err := runCommand(&Context{WorkDir: t.TempDir()}, map[string]interface{}{
		"command":    "cmd /c exit 0",
		"background": true,
		"timeout":    30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, (30 * time.Second).String()) {
		t.Fatalf("显式 timeout 未生效：%q", res.Output)
	}
}
