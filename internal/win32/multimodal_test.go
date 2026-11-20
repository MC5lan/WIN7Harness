//go:build windows

package win32

import (
	"syscall"
	"testing"
)

// TestOpenFileFilter 回归：OPENFILENAMEW 的过滤器必须是
// "显示名\0模式\0显示名\0模式\0\0" 的多字符串结构。
// 老实现把含 NUL 的整串直接交给 syscall.StringToUTF16，
// 点「选图」/「选证书」按钮时会 panic（string with NUL passed to StringToUTF16）。
func TestOpenFileFilter(t *testing.T) {
	f := openFileFilter("图片文件 (*.png)", "*.png", "所有文件 (*.*)", "*.*")
	if len(f) < 2 {
		t.Fatalf("过滤器过短: %d", len(f))
	}
	if f[len(f)-1] != 0 || f[len(f)-2] != 0 {
		t.Fatalf("过滤器必须以双 NUL 结尾，实际尾部: %v", f[len(f)-4:])
	}
	var parts []string
	start := 0
	for i, c := range f {
		if c != 0 {
			continue
		}
		if i == start { // 连续两个 NUL = 结束
			break
		}
		parts = append(parts, syscall.UTF16ToString(f[start:i]))
		start = i + 1
	}
	want := []string{"图片文件 (*.png)", "*.png", "所有文件 (*.*)", "*.*"}
	if len(parts) != len(want) {
		t.Fatalf("段数 = %d，期望 %d：%q", len(parts), len(want), parts)
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Fatalf("第 %d 段 = %q，期望 %q", i, parts[i], want[i])
		}
	}
}
