package core

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	logMu    sync.Mutex
	logFile  *os.File
	logLines []string
	logMax   = 400
)

// InitLog 打开日志文件（失败也不致命，只影响日志查看）。
func InitLog() {
	logMu.Lock()
	defer logMu.Unlock()
	_ = os.MkdirAll(LocalDataDir(), 0o755)
	f, err := os.OpenFile(LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		logFile = f
	}
}

// Logf 记录一行日志，同时进入内存环形队列供 UI 展示。
func Logf(format string, args ...interface{}) {
	line := fmt.Sprintf(format, args...)
	stamp := time.Now().Format("2006-01-02 15:04:05")
	full := stamp + "  " + line
	logMu.Lock()
	logLines = append(logLines, full)
	if len(logLines) > logMax {
		logLines = logLines[len(logLines)-logMax:]
	}
	if logFile != nil {
		_, _ = logFile.WriteString(full + "\r\n")
	}
	logMu.Unlock()
}

// RecentLogs 返回最近的日志行（供设置页展示）。
func RecentLogs() []string {
	logMu.Lock()
	defer logMu.Unlock()
	out := make([]string, len(logLines))
	copy(out, logLines)
	return out
}

// CloseLog 关闭日志句柄。
func CloseLog() {
	logMu.Lock()
	defer logMu.Unlock()
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
}

// LogTail 读取日志文件末尾 n 行；文件不可读时退回内存队列。
func LogTail(n int) []string {
	if n <= 0 {
		n = 200
	}
	data, err := os.ReadFile(LogPath())
	if err != nil {
		return RecentLogs()
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// LogSize 返回日志文件大小（字节）。
func LogSize() int64 {
	if info, err := os.Stat(LogPath()); err == nil {
		return info.Size()
	}
	return 0
}

// RotateLogIfBig 日志超过 max 字节时轮转为 harness.log.1，避免长期运行撑满磁盘。
func RotateLogIfBig(max int64) bool {
	if LogSize() <= max {
		return false
	}
	logMu.Lock()
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
	_ = os.Remove(LogPath() + ".1")
	if err := os.Rename(LogPath(), LogPath()+".1"); err != nil {
		logMu.Unlock()
		return false
	}
	if f, err := os.OpenFile(LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		logFile = f
	}
	logMu.Unlock()
	// 注意：Logf 会再次获取 logMu，必须在解锁之后调用，否则自死锁。
	Logf("日志超过 %d KB，已轮转为 harness.log.1", max/1024)
	return true
}
