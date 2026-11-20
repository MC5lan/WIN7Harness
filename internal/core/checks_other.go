//go:build !windows

package core

import (
	"runtime"
	"syscall"
)

func platformChecks() []CheckResult {
	return []CheckResult{
		{Name: "系统版本", Status: CheckOK, Detail: runtime.GOOS + "/" + runtime.GOARCH + "（开发机，非目标平台）"},
	}
}

// OSVersion 返回当前系统标识；非 Windows 开发机只用于报告。
func OSVersion() string { return osVersion() }

func osVersion() string { return runtime.GOOS }

func diskFree(dir string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	bs := uint64(st.Bsize)
	return st.Bavail * bs, st.Blocks * bs, nil
}
