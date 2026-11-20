//go:build !windows

package tools

import (
	"strings"
	"testing"
	"time"
)

// 验证钩子命令确实能拿到 HARNESS_* 环境变量。
func TestHookEnvInjection(t *testing.T) {
	cmdline := withHookEnv(`echo "$HARNESS_EVENT|$HARNESS_TOOL|$HARNESS_WORKDIR"`, HookPreTool, "write_file", "/tmp")
	out, code, err := runShell(cmdline, "/tmp", 10*time.Second, nil)
	if err != nil && out == "" {
		t.Fatalf("执行失败: %v", err)
	}
	if code != 0 {
		t.Fatalf("退出码 %d", code)
	}
	if !strings.Contains(out, "pre_tool|write_file|/tmp") {
		t.Fatalf("环境变量未注入，输出: %q", out)
	}
}

// 验证非零退出码被识别为阻止信号（RunHooks 的判定依据）。
func TestHookExitCode(t *testing.T) {
	_, code, _ := runShell("exit 3", "/tmp", 10*time.Second, nil)
	if code != 3 {
		t.Fatalf("退出码应为 3，实际 %d", code)
	}
}
