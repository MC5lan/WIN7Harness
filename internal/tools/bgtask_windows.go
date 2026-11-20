//go:build windows && (amd64 || 386)

package tools

import (
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"win7harness/internal/core"
)

// bgProc 是后台任务的进程句柄。Windows 上挂 Job Object，
// 这样 kill 一次就能连带清理 cmd.exe 派生的所有后代进程。
type bgProc struct {
	cmd *exec.Cmd
	job uintptr
	// jobOK 记录 Job Object 是否真的绑上了。
	// Win7 不支持嵌套 Job：如果本进程已经在某个 Job 里（被启动器/IDE 托管时很常见），
	// AssignProcessToJobObject 会失败，此时 TerminateJobObject 杀的是个空 Job。
	// 过去这个返回值直接被丢掉，于是"看起来杀过了，其实一个进程都没死"。
	jobOK bool
	once  sync.Once
}

type sinkWriter struct{ fn func([]byte) }

func (w sinkWriter) Write(p []byte) (int, error) {
	w.fn(p)
	return len(p), nil
}

func startBgProc(cmdline, cwd string, sink func([]byte)) (*bgProc, error) {
	cmd := shellCommand(cmdline)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append(os.Environ(),
		"PYTHONIOENCODING=utf-8",
		"GIT_PAGER=cat",
		"PAGER=cat",
	)
	cmd.Env = append(cmd.Env, childNetEnv()...)
	// 后台任务的 stdout/stderr 是 writer，os/exec 会起内部 goroutine 拷贝管道。
	// 如果命令派生的孙进程继承了写端（dev server、watcher 很常见），cmd.Wait()
	// 会一直等管道 EOF，任务状态永远停在"运行中"——用户看到的就是"后台卡死"。
	// WaitDelay 让进程退出后最多再等 3 秒，然后强制收掉管道。
	cmd.WaitDelay = 3 * time.Second
	w := sinkWriter{sink}
	cmd.Stdout = w
	cmd.Stderr = w

	// 与 exec_windows.go 一致：先建 Job 再 Start，缩短孙进程逃逸窗口；
	// 每一步失败都记日志——过去这些返回值被静默丢弃，杀树失败时毫无诊断。
	job, _, _ := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		core.Logf("tools: 后台任务 CreateJobObject 失败，杀树靠 taskkill /T 兜底")
	} else if !setChildJobLimits(job) {
		core.Logf("tools: 后台任务 SetInformationJobObject 失败，内存/进程数限制与 KillOnClose 均未生效")
	}

	if err := cmd.Start(); err != nil {
		if job != 0 {
			syscall.CloseHandle(syscall.Handle(job))
		}
		return nil, err
	}
	p := &bgProc{cmd: cmd}
	if job != 0 {
		if ok, _, _ := procAssignProcessToJobObj.Call(job, uintptr(cmd.Process.Pid)); ok != 0 {
			p.job = job
			p.jobOK = true
		} else {
			core.Logf("tools: 后台任务 AssignProcessToJobObject 失败（进程可能已在别的 Job 中），杀树靠 taskkill /T 兜底")
			// 绑不上就别留这个句柄：kill 时按"没有 Job"处理，走 taskkill /T 兜底。
			syscall.CloseHandle(syscall.Handle(job))
		}
	}
	return p, nil
}

func (p *bgProc) wait() int {
	err := p.cmd.Wait()
	p.once.Do(func() {
		if p.job != 0 {
			syscall.CloseHandle(syscall.Handle(p.job))
			p.job = 0
		}
	})
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

func (p *bgProc) kill() {
	// 顺序很重要：先 taskkill /T 按 PID 枚举整棵进程树，再收拾 Job。
	//
	// 只用 TerminateJobObject 有两个坑：① Job 可能根本没绑上（见 jobOK）；
	// ② 后台任务的 stdout 是管道，cmd.exe 派生的孙进程（ping、go run 的子进程）
	//    继承了写端，只杀 cmd.exe 的话管道不会 EOF，cmd.Wait() 永不返回，
	//    任务状态就永远停在"运行中"——ST45 报的正是这一条。
	// taskkill /F /T 会把整棵树一起收掉，管道写端全部关闭，Wait 才能真正返回。
	if p.cmd.Process != nil {
		killProcessTree(p.cmd.Process.Pid)
	}
	if p.jobOK && p.job != 0 {
		procTerminateJobObject.Call(p.job, 1)
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// killProcessTree 用系统自带的 taskkill 结束整棵进程树。
// 不自己枚举子进程：Win7 上没有 "GetChildProcessList" 这类 API，
// 走 WMI 太重且慢；taskkill /T 就是为这件事做的，且系统一定有。
func killProcessTree(pid int) {
	c := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid))
	// 静默执行：本程序是 GUI 子系统，直接跑控制台程序会闪一个黑框。
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	_ = c.Run()
}
