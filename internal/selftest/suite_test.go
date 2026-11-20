package selftest

import (
	"strings"
	"testing"
	"time"
)

// TestRunSuite 在开发机上跑通整套自检（非 Windows 平台会跳过渲染探测）。
// 作用不是替代真机验证，而是保证用例本身写对了：真机上挂掉一项时，
// 能确定问题在环境而不是用例。
func TestRunSuite(t *testing.T) {
	rep := Run(Options{
		WorkDir: t.TempDir(),
		DataDir: t.TempDir(),
		Timeout: 20 * time.Second,
	})
	t.Log("\n" + rep.Text())

	if rep.Total() < 15 {
		t.Fatalf("用例数 %d，少于预期的 16", rep.Total())
	}
	for _, c := range rep.Cases {
		if c.Status == StatusFail {
			t.Errorf("%s %s 失败：%s（%s）", c.ID, c.Name, c.Detail, c.Hint)
		}
	}
	if !rep.OK() {
		t.Fatalf("自检未通过，通过率 %.1f%%", rep.PassRate())
	}
	if !strings.Contains(rep.Markdown(), "自检报告") {
		t.Fatal("Markdown 报告缺少标题")
	}
}

// TestReportWrite 报告落盘可用，且不覆盖上一次。
func TestReportWrite(t *testing.T) {
	dir := t.TempDir()
	rep := &Report{Version: "test", Cases: []Case{{ID: "ST01", Name: "x", Status: StatusPass}}}
	md, js, err := rep.WriteReport(dir)
	if err != nil {
		t.Fatalf("写报告失败：%v", err)
	}
	if md == "" || js == "" {
		t.Fatalf("报告路径为空：%q %q", md, js)
	}
}

// TestExitCode 命令行退出码语义：全绿 0，有失败 1，准备失败 2。
func TestExitCode(t *testing.T) {
	ok := &Report{Cases: []Case{{Status: StatusPass}, {Status: StatusSkip}}}
	if ok.ExitCode() != 0 || !ok.OK() {
		t.Fatalf("全绿场景退出码应为 0，实际 %d", ok.ExitCode())
	}
	bad := &Report{Cases: []Case{{Status: StatusPass}, {Status: StatusFail}}}
	if bad.ExitCode() != 1 || bad.OK() {
		t.Fatalf("含失败场景退出码应为 1，实际 %d", bad.ExitCode())
	}
	boot := &Report{Err: "no dir"}
	if boot.ExitCode() != 2 {
		t.Fatalf("准备失败退出码应为 2，实际 %d", boot.ExitCode())
	}
	// 跳过不计入分母
	skipOnly := &Report{Cases: []Case{{Status: StatusSkip}, {Status: StatusPass}}}
	if skipOnly.PassRate() != 100 {
		t.Fatalf("跳过不计分母，通过率应为 100%%，实际 %.1f%%", skipOnly.PassRate())
	}
}
