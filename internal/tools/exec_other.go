//go:build !windows

package tools

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
	"win7harness/internal/core"
)

// runShell 非 Windows 平台的等价实现，仅用于开发期交叉验证。
func runShell(cmdline, cwd string, timeout time.Duration, extraEnv []string, cancelCh ...<-chan struct{}) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// 外部取消并入同一 ctx：收到取消信号即终止命令
	for _, ch := range cancelCh {
		if ch == nil {
			continue
		}
		ch := ch
		go func() {
			select {
			case <-ch:
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdline)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append(cmd.Environ(), "PYTHONIOENCODING=utf-8", "GIT_PAGER=cat", "PAGER=cat")
	cmd.Env = append(cmd.Env, extraEnv...)

	outR, err := cmd.StdoutPipe()
	if err != nil {
		return "", -1, err
	}
	errR, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return "", -1, err
	}
	buf := newLimited(maxOutputBytes)
	var wg sync.WaitGroup
	wg.Add(2)
	core.SafeGo("tools/exec_other.go:36", func() { defer wg.Done(); io.Copy(buf, outR) })
	core.SafeGo("tools/exec_other.go:37", func() { defer wg.Done(); io.Copy(buf, errR) })

	// 必须先等读取完再 Wait：cmd.Wait 会关闭 StdoutPipe/StderrPipe，
	// 与并发 io.Copy 撞上会丢输出（表现为"退出码 0 但无输出"的偶发失败）。
	wg.Wait()
	waitErr := cmd.Wait()

	code := 0
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	text := buf.String()
	if ctx.Err() == context.DeadlineExceeded {
		text += fmt.Sprintf("\n\n[超时] 命令执行超过 %s，已被强制终止", timeout)
		if code == 0 {
			code = -2
		}
	}
	return text, code, nil
}
