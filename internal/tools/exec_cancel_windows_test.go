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
	// 命令本身要跑约 19 秒，取消后应在 8 秒内返回。3 秒在慢速 386 CI
	// runner 上会偶发误报（8 秒仍远小于 15 秒超时，足以证明取消生效）。
	if err != nil && elapsed > 8*time.Second {
		t.Fatalf("取消后仍在运行：%v（%s）", err, elapsed)
	}
	if elapsed > 8*time.Second {
		t.Fatalf("取消信号未及时杀树：%s", elapsed)
	}
}
