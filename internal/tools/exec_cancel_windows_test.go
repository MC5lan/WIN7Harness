//go:build windows

package tools

import (
	"context"
	"testing"
	"time"
)

// 用户点“停止”时必须杀掉 run_command 的整棵进程树；之前 tools.Context
// 根本没有注入 Ctx，取消信号传不到 runShell，鼠标/自动化脚本会继续跑。
func TestRunShellCancelKillsTree(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, _, err := runShell("ping -n 20 127.0.0.1 >nul", "", 15*time.Second, nil, ctx.Done())
	elapsed := time.Since(start)
	if err != nil && elapsed > 3*time.Second {
		t.Fatalf("取消后仍在运行：%v（%s）", err, elapsed)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("取消信号未及时杀树：%s", elapsed)
	}
}
