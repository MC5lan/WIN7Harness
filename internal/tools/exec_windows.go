//go:build windows && (amd64 || 386)

package tools

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"win7harness/internal/core"
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW      = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObj  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObj = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject    = kernel32.NewProc("TerminateJobObject")
)

const (
	createNoWindow             = 0x08000000
	createNewProcessGroup      = 0x00000200
	jobObjectLimitKillOnClose  = 0x2000
	jobObjectLimitProcessMem   = 0x00000100
	jobObjectLimitJobMem       = 0x00000200
	jobObjectLimitActiveProc   = 0x00000008
	jobObjectLimitPriority     = 0x00000020
	belowNormalPriorityClass   = 0x00004000
	jobObjectExtendedLimitInfo = 9
)

type jobBasicLimit struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

// childJobLimits 给工具子进程一套与机器档位匹配的上限：
//   - 单个进程不能无限吃内存，否则 2GB Win7 会被一个 go test 拖死；
//   - 整个 Job 也有总上限，防止并行子任务叠加；
//   - 进程数上限挡住 fork 炸弹/失控构建；
//   - BelowNormal 优先级让 UI 在 2 核老机上仍有响应。
func childJobLimits() (procMem, jobMem uintptr) {
	switch {
	case runtime.GOARCH == "386":
		return 512 << 20, 1200 << 20
	case core.DetectProfile().Tier == "2G":
		return 768 << 20, 1400 << 20
	case core.DetectProfile().Tier == "4G":
		return 1 << 30, 2 << 30
	default:
		// 32 位编译目标下 uintptr 存不下 4GB 常量，统一封顶 3GB。
		return 2 << 30, 3 << 30
	}
}

func configureChildJob(info *jobExtendedLimit) {
	procMem, jobMem := childJobLimits()
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnClose |
		jobObjectLimitProcessMem | jobObjectLimitJobMem |
		jobObjectLimitActiveProc | jobObjectLimitPriority
	info.BasicLimitInformation.ActiveProcessLimit = 64
	info.BasicLimitInformation.PriorityClass = belowNormalPriorityClass
	info.ProcessMemoryLimit = procMem
	info.JobMemoryLimit = jobMem
}

func setJobInfo(job uintptr, info *jobExtendedLimit) bool {
	ok, _, err := procSetInformationJobObj.Call(job,
		uintptr(jobObjectExtendedLimitInfo),
		uintptr(unsafe.Pointer(info)),
		unsafe.Sizeof(*info))
	if ok == 0 {
		core.Logf("tools: SetInformationJobObject 失败（size=%d flags=0x%x proc=%d job=%d）：%v",
			unsafe.Sizeof(*info), info.BasicLimitInformation.LimitFlags,
			info.ProcessMemoryLimit, info.JobMemoryLimit, err)
	}
	return ok != 0
}

// setChildJobLimits 设置完整限额；若系统拒绝（极老/受限环境），
// 至少退回 KILL_ON_JOB_CLOSE，保证父进程退出时能带走整棵进程树。
func setChildJobLimits(job uintptr) bool {
	info := jobExtendedLimit{}
	configureChildJob(&info)
	if setJobInfo(job, &info) {
		return true
	}
	fallback := jobExtendedLimit{}
	fallback.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnClose
	return setJobInfo(job, &fallback)
}

// decodeConsoleText 把 cmd.exe / PowerShell 的管道输出转成 UTF-8。
//
// 中文 Win7 的 cmd 内置命令（dir/where/type...）按 OEM 代码页 936（GBK）写管道，
// 直接当 UTF-8 用会把中文变成 “????” 或 U+FFFD，模型就会误判成路径损坏、
// 反复换命令重试——这是“工程能力奇怪”的一个真实来源。
// 已经是合法 UTF-8 的输出（Go/Python/Node 等）原样保留，避免二次转码。
func decodeConsoleText(s string) string {
	if s == "" || utf8.ValidString(s) {
		return s
	}
	if out, _, err := transform.String(simplifiedchinese.GBK.NewDecoder(), s); err == nil {
		return out
	}
	return strings.ToValidUTF8(s, "�")
}

// shellCommand 构造 cmd.exe 调用，关键是用 SysProcAttr.CmdLine 原样传命令串。
//
// 不能用 exec.Command("cmd.exe", "/d", "/s", "/c", cmdline)：Windows 下
// os/exec 会给参数做 Go 风格转义，命令里的 "D:\路径" 会变成 `\"D:\路径\"`，
// cmd 收到后报“文件名、目录名或卷标语法不正确”。这正是模型带引号路径
// 反复失败、看起来“工程能力很差”的根因。CmdLine 绕过二次转义，
// /s + 外层一对引号则让 cmd 只剥最外层，保留内部引号。
// mergeEnvWindows 按 Windows 大小写不敏感规则合并环境变量，后写入者覆盖先写入者。
func mergeEnvWindows(groups ...[]string) []string {
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	out := make([]string, 0, total)
	index := map[string]int{}
	add := func(kv string) {
		if kv == "" {
			return
		}
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			// Windows 的 "=C:=C:\dir" 每驱动器当前目录项没有常规 KEY=VALUE 形式，
			// 原样保留，不要因为去重把它丢掉。
			out = append(out, kv)
			return
		}
		key := strings.ToUpper(kv[:eq])
		if i, ok := index[key]; ok {
			out[i] = kv
			return
		}
		index[key] = len(out)
		out = append(out, kv)
	}
	for _, g := range groups {
		for _, kv := range g {
			add(kv)
		}
	}
	return out
}

func shellCommand(cmdline string) *exec.Cmd {
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | createNewProcessGroup,
		CmdLine:       `cmd.exe /d /s /c "` + cmdline + `"`,
	}
	return cmd
}

// runShell 用 cmd.exe 执行命令行，进程挂到 Job Object 上，超时或外部取消杀整棵进程树。
// extraEnv 追加到进程环境（而不是拼进命令行）。
func runShell(cmdline, cwd string, timeout time.Duration, extraEnv []string, cancelCh ...<-chan struct{}) (string, int, error) {
	cmd := shellCommand(cmdline)
	if cwd != "" {
		cmd.Dir = cwd
	}
	baseEnv := append(os.Environ(),
		"PYTHONIOENCODING=utf-8",
		"GIT_PAGER=cat",
		"PAGER=cat",
	)
	// 操作系统代理/系统 CA 先注入；调用方显式 extraEnv 仍可覆盖。
	// 环境变量名在 Windows 上大小写不敏感，必须去重后写入，否则父进程的
	// 旧 HTTP_PROXY 可能和本机桥地址同时存在，子进程取到哪个不确定。
	cmd.Env = mergeEnvWindows(baseEnv, childNetEnv(), extraEnv)

	outR, outW, err := os.Pipe()
	if err != nil {
		return "", -1, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return "", -1, err
	}
	cmd.Stdout = outW
	cmd.Stderr = errW
	cmd.Stdin = nil

	// Job Object 在 Start 之前建好并设限制（这两步不依赖子进程），
	// 这样 Start 与 Assign 之间只剩一次 syscall，孙进程逃逸窗口降到最小。
	// 绑不上的情况必须知道：Win7 不支持嵌套 Job，本进程若已在别的 Job 里，
	// Assign 会失败，此时 TerminateJobObject 杀的是空 Job，必须靠 taskkill 兜底。
	job, _, _ := procCreateJobObjectW.Call(0, 0)
	jobOK := false
	if job == 0 {
		core.Logf("tools: CreateJobObject 失败，杀树只能靠 taskkill /T 兜底")
	} else if !setChildJobLimits(job) {
		core.Logf("tools: SetInformationJobObject 失败，内存/进程数限制与 KillOnClose 均未生效")
	}

	if err := cmd.Start(); err != nil {
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
		if job != 0 {
			syscall.CloseHandle(syscall.Handle(job))
		}
		return "", -1, err
	}
	// 父进程必须立即关闭写端，否则读端永远等不到 EOF
	outW.Close()
	errW.Close()

	if job != 0 {
		if ok, _, _ := procAssignProcessToJobObj.Call(job, uintptr(cmd.Process.Pid)); ok != 0 {
			jobOK = true
		} else {
			core.Logf("tools: AssignProcessToJobObject 失败（进程可能已在别的 Job 中），杀树靠 taskkill /T 兜底")
		}
	}

	// 超时与外部取消共用同一条杀树路径
	killTree := func() {
		if cmd.Process != nil {
			killProcessTree(cmd.Process.Pid)
		}
		if jobOK && job != 0 {
			procTerminateJobObject.Call(job, 1)
		}
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}

	buf := newLimited(maxOutputBytes)
	var wg sync.WaitGroup
	wg.Add(2)
	core.SafeGo("tools/exec_windows.go:122", func() { defer wg.Done(); io.Copy(buf, outR) })
	core.SafeGo("tools/exec_windows.go:123", func() { defer wg.Done(); io.Copy(buf, errR) })

	done := make(chan struct{})
	var timedOut atomic.Bool
	core.SafeGo("tools/exec_windows.go:127", func() {
		select {
		case <-done:
		case <-time.After(timeout):
			timedOut.Store(true)
			// 先按 PID 收整棵树：构建/测试命令常常派生孙进程，
			// 只杀 cmd.exe 会留下孤儿占着文件与端口（下一次构建就报"文件被占用"）。
			killTree()
		}
	})
	// 外部取消（用户点了“停止”）：杀掉整棵进程树，别让构建/测试在后台空转。
	if len(cancelCh) > 0 && cancelCh[0] != nil {
		core.SafeGo("tools/exec_windows.go:cancel", func() {
			select {
			case <-cancelCh[0]:
				killTree()
			case <-done:
			}
		})
	}

	waitErr := cmd.Wait()
	close(done)
	// 立即关闭读端：继承了写端的孙进程不会再挡住 EOF，
	// io.Copy 收到错误退出，wg.Wait() 必然返回，
	// 否则 "超时/命令结束后工具永远不返回"。
	outR.Close()
	errR.Close()
	wg.Wait()
	if job != 0 {
		syscall.CloseHandle(syscall.Handle(job))
	}

	code := 0
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	text := decodeConsoleText(buf.String())
	if buf.truncated {
		text += fmt.Sprintf("\n\n[输出已截断：只保留前 %dKB。完整日志请重定向到文件后用 read_file 分段读，或用 background=true + task_output 增量读]", maxOutputBytes/1024)
	}
	if timedOut.Load() {
		text += fmt.Sprintf("\n\n[超时] 命令执行超过 %s，已被强制终止（含全部子进程）", timeout)
		if code == 0 {
			code = -2
		}
	}
	low := strings.ToLower(text)
	if strings.Contains(low, "407") && strings.Contains(low, "proxy") {
		text += "\n\n[代理认证] 上游代理返回 407。环境变量代理不支持 NTLM/Kerberos 集成认证；请改用 Basic 代理、VPN/透明网关，或等待后续 SSPI 本机转发桥。不要反复重试同一条命令。"
	}
	return text, code, nil
}
