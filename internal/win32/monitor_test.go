//go:build windows

package win32

import "testing"

func TestMonitorWorkAreaAtPrimary(t *testing.T) {
	work, ok := MonitorWorkAreaAt(0, 0)
	if !ok || work.Width() <= 0 || work.Height() <= 0 {
		t.Fatalf("主显示器工作区异常：%+v ok=%v", work, ok)
	}
}
