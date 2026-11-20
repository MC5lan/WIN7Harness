//go:build !windows

package tools

import (
	"os"
	"os/exec"
	"syscall"
)

// bgProc 是后台任务的进程句柄（非 Windows 平台，仅用于本机开发与测试）。
type bgProc struct {
	cmd *exec.Cmd
}

type sinkWriter struct{ fn func([]byte) }

func (w sinkWriter) Write(p []byte) (int, error) {
	w.fn(p)
	return len(p), nil
}

func startBgProc(cmdline, cwd string, sink func([]byte)) (*bgProc, error) {
	cmd := exec.Command("/bin/sh", "-c", cmdline)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append(os.Environ(), "PAGER=cat", "GIT_PAGER=cat")
	// 独立进程组，kill 时能整组带走。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	w := sinkWriter{sink}
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &bgProc{cmd: cmd}, nil
}

func (p *bgProc) wait() int {
	err := p.cmd.Wait()
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

func (p *bgProc) kill() {
	if p.cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	_ = p.cmd.Process.Kill()
}
