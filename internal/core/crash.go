package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// 崩溃可观测：把人最需要的那几件事留成文件，而不是只弹一个"程序出错了"。
//
// 三件事：
//  1. MarkRunning / MarkCleanExit —— 下次启动能判断出"上次是异常退出"，
//     因为正常退出会写 clean_exit=true。用户报"它自己关了"时这是关键线索。
//  2. WriteCrashReport —— panic 现场单独落一份报告：版本、调用栈、日志尾部、
//     环境摘要、脱敏配置。只靠 harness.log 定位不了问题。
//  3. ListCrashReports —— 让界面和 /crash 命令能把报告给出来。

// RunMark 记录一次运行的开始与结束状态，用于判断上次是否异常退出。
type RunMark struct {
	PID         int       `json:"pid"`
	Version     string    `json:"version"`
	StartedAt   time.Time `json:"started_at"`
	CleanExit   bool      `json:"clean_exit"`
	ExitCode    int       `json:"exit_code"`
	CrashReport string    `json:"crash_report,omitempty"`
}

func runMarkPath() string { return filepath.Join(LocalDataDir(), "last_run.json") }

// CrashDir 崩溃报告目录。
func CrashDir() string { return filepath.Join(LocalDataDir(), "crash") }

// MarkRunning 在启动时调用，写下"我正在跑"的标记。
func MarkRunning(version string) {
	m := RunMark{PID: os.Getpid(), Version: version, StartedAt: time.Now()}
	if b, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.MkdirAll(LocalDataDir(), 0o755)
		_ = WriteFileAtomic(runMarkPath(), b, 0o644)
	}
}

// MarkCleanExit 在正常退出时调用，覆盖标记为"已干净退出"。
func MarkCleanExit(code int) {
	m := RunMark{PID: os.Getpid(), Version: Version, StartedAt: time.Now(), CleanExit: true, ExitCode: code}
	if b, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = WriteFileAtomic(runMarkPath(), b, 0o644)
	}
}

// DetectAbnormalExit 判断上次是否异常退出，返回上次的运行标记。
// 没有标记（首次运行）或上次已干净退出时返回 false。
func DetectAbnormalExit() (RunMark, bool) {
	var m RunMark
	data, err := os.ReadFile(runMarkPath())
	if err != nil {
		return m, false
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, false
	}
	if m.CleanExit {
		return m, false
	}
	// 同一进程内重复调用（比如重载）不算异常
	if m.PID == os.Getpid() {
		return m, false
	}
	return m, true
}

// WriteCrashReport 写一份崩溃报告并返回路径。
// kind 用来区分 panic / 引擎异常 / 子 goroutine 崩溃，detail 通常是调用栈。
func WriteCrashReport(kind, detail string) (string, error) {
	if err := os.MkdirAll(CrashDir(), 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("crash-%s-%09d.txt", time.Now().Format("20060102-150405"), time.Now().UnixNano()%1000000000)
	path := filepath.Join(CrashDir(), name)

	var b strings.Builder
	fmt.Fprintf(&b, "WIN7 Harness 异常报告\r\n")
	fmt.Fprintf(&b, "时间: %s\r\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "版本: %s\r\n", Version)
	fmt.Fprintf(&b, "类型: %s\r\n", kind)
	fmt.Fprintf(&b, "进程: pid=%d  goroutine=%d\r\n", os.Getpid(), runtime.NumGoroutine())
	fmt.Fprintf(&b, "Go: %s  %s/%s\r\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "\r\n----- 现场明细 -----\r\n%s\r\n", detail)
	fmt.Fprintf(&b, "\r\n----- 环境 -----\r\n%s\r\n", envDump())
	fmt.Fprintf(&b, "\r\n----- 配置（已脱敏） -----\r\n%s\r\n", RedactedConfigJSON())
	fmt.Fprintf(&b, "\r\n----- 日志尾部 -----\r\n")
	for _, line := range LogTail(300) {
		b.WriteString(line + "\r\n")
	}
	if err := WriteFileAtomic(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	Logf("已生成异常报告: %s", path)

	// 顺手把标记里的报告路径更新，下次启动能直接提示"上次崩在这里"。
	var m RunMark
	if data, err := os.ReadFile(runMarkPath()); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	m.CrashReport = path
	if data, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = WriteFileAtomic(runMarkPath(), data, 0o644)
	}
	return path, nil
}

// ListCrashReports 返回最近的崩溃报告路径（新→旧）。
func ListCrashReports(limit int) []string {
	ents, err := os.ReadDir(CrashDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		out = append(out, filepath.Join(CrashDir(), e.Name()))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// PruneCrashReports 只保留最近 keep 份报告，避免反复崩溃把磁盘塞满。
func PruneCrashReports(keep int) int {
	if keep <= 0 {
		keep = 10
	}
	list := ListCrashReports(0)
	removed := 0
	for i := keep; i < len(list); i++ {
		if err := os.Remove(list[i]); err == nil {
			removed++
		}
	}
	return removed
}

// startupWarning 保存启动时算好的"上次异常退出"提示。
// 因为 MarkRunning 会立刻把 PID 换成自己，之后再也判断不出上次的异常，
// 所以必须在启动最早期算好、由界面读取。
var startupWarning atomic.Value // string

// SetStartupWarning 记录启动提示（main 里检测完成后调用）。
func SetStartupWarning(s string) { startupWarning.Store(s) }

// StartupWarning 返回启动时应提示给用户的内容。
func StartupWarning() string {
	if s, ok := startupWarning.Load().(string); ok {
		return s
	}
	return ""
}

// AbnormalExitHint 生成给用户看的一句话提示；没有异常时返回空串。
func AbnormalExitHint() (string, string) {
	m, abnormal := DetectAbnormalExit()
	if !abnormal {
		return "", ""
	}
	when := m.StartedAt.Format("01-02 15:04")
	hint := fmt.Sprintf("上次（%s 启动，v%s）没有正常退出，进程可能被强制结束或崩溃。", when, m.Version)
	if m.CrashReport != "" {
		if _, err := os.Stat(m.CrashReport); err == nil {
			hint += "\r\n异常报告：" + m.CrashReport
		}
	}
	hint += "\r\n如需排查，输入 /crash 查看最近的异常报告。"
	return hint, m.CrashReport
}
