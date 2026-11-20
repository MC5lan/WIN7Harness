package core

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestAbnormalExitDetection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)

	// 首次运行：没有标记，不算异常
	if _, ok := DetectAbnormalExit(); ok {
		t.Fatal("无标记时不应判定为异常退出")
	}

	// 标记为"正在运行"但没写干净退出 → 下次启动应识别为异常
	// 模拟"上次是另一个进程"：PID 与当前进程不同
	writeFakeMark(t, 999999, false, "")
	m, ok := DetectAbnormalExit()
	if !ok {
		t.Fatal("未干净退出应被识别")
	}
	if m.Version != Version {
		t.Fatalf("版本记录不对: %q", m.Version)
	}

	writeFakeMark(t, 999999, true, "")
	if _, ok := DetectAbnormalExit(); ok {
		t.Fatal("干净退出后不应再报异常")
	}
	// 本进程自己写的标记不该被当成上一次性命（避免重入误报）
	MarkRunning(Version)
	if _, ok := DetectAbnormalExit(); ok {
		t.Fatal("同进程标记不应判定为异常")
	}
}

func TestWriteCrashReport(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	InitLog()
	defer CloseLog()
	Logf("测试日志行 A")
	writeFakeMark(t, 999999, false, "")

	path, err := WriteCrashReport("panic", "goroutine 1: 测试栈")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{"测试栈", "测试日志行 A", "版本", "配置（已脱敏）"} {
		if !strings.Contains(body, want) {
			t.Fatalf("报告缺少内容 %q", want)
		}
	}

	list := ListCrashReports(5)
	if len(list) != 1 || list[0] != path {
		t.Fatalf("报告列表不对: %v", list)
	}

	// 标记里应带上报告路径
	m, _ := DetectAbnormalExit()

	if m.CrashReport != path {
		t.Fatalf("标记未记录报告路径: %q", m.CrashReport)
	}
	hint, rp := AbnormalExitHint()
	if !strings.Contains(hint, "/crash") || rp != path {
		t.Fatalf("提示生成不对: %q / %q", hint, rp)
	}
}

func TestPruneCrashReports(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	if err := os.MkdirAll(CrashDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"crash-20260101-000001.txt", "crash-20260102-000001.txt", "crash-20260103-000001.txt"} {
		if err := os.WriteFile(CrashDir()+string(os.PathSeparator)+n, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(ListCrashReports(0)); got != 3 {
		t.Fatalf("应有 3 份报告，实际 %d", got)
	}
	if removed := PruneCrashReports(1); removed != 2 {
		t.Fatalf("应清理 2 份，实际 %d", removed)
	}
	list := ListCrashReports(0)
	if len(list) != 1 || !strings.Contains(list[0], "20260103") {
		t.Fatalf("应保留最新一份: %v", list)
	}
}

// writeFakeMark 直接写一个"上一条命"的运行标记，PID 故意与当前进程不同。
func writeFakeMark(t *testing.T, pid int, clean bool, report string) {
	t.Helper()
	body := `{"pid":` + strconv.Itoa(pid) + `,"version":"` + Version + `","started_at":"2026-01-02T03:04:05Z","clean_exit":` +
		map[bool]string{true: "true", false: "false"}[clean] + `,"exit_code":0`
	if report != "" {
		body += `,"crash_report":"` + report + `"`
	}
	body += "}"
	if err := os.WriteFile(runMarkPath(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
