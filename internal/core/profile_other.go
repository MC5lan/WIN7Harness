//go:build !windows

package core

import (
	"os"
	"strconv"
	"strings"
)

// totalMemoryKB 开发机上读 /proc/meminfo，只为让自检能跑到真实分支。
func totalMemoryKB() (uint64, bool) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			return 0, false
		}
		kb, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return kb, true
	}
	return 0, false
}
