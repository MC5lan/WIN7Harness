//go:build windows

package win32

import "unsafe"

// Job Object 辅助：把子进程挂到 KILL_ON_JOB_CLOSE 的 Job 里，
// 父进程（含崩溃）退出时 Windows 自动杀掉整棵进程树。
//
// 注意 Win7 语义：嵌套 Job 不支持——如果本进程已经在别的 Job 里
// （被启动器/IDE 托管时很常见），AssignProcessToJobObject 会失败。
// 失败时返回 false 并关闭句柄，调用方必须走 taskkill /T 兜底，
// 不能把“以为绑上了、其实空 Job”当成有效防护。

var (
	pCreateJobObjectW      = dKernel32.NewProc("CreateJobObjectW")
	pSetInformationJobObj  = dKernel32.NewProc("SetInformationJobObject")
	pAssignProcessToJobObj = dKernel32.NewProc("AssignProcessToJobObject")
	pTerminateJobObject    = dKernel32.NewProc("TerminateJobObject")
)

const (
	jobObjectLimitKillOnClose  = 0x2000
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

type jobExtendedLimit struct {
	BasicLimitInformation jobBasicLimit
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// AssignKillOnCloseJob 把 pid 挂到新建的 KILL_ON_JOB_CLOSE Job。
// 返回 job 句柄（调用方负责 CloseHandle）与是否成功。
func AssignKillOnCloseJob(pid int) (uintptr, bool) {
	job, _, _ := pcall(pCreateJobObjectW, 0, 0)
	if job == 0 {
		return 0, false
	}
	info := jobExtendedLimit{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnClose
	if r, _, _ := pcall(pSetInformationJobObj, job,
		uintptr(jobObjectExtendedLimitInfo),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info)); r == 0 {
		CloseHandle(job)
		return 0, false
	}
	if ok, _, _ := pcall(pAssignProcessToJobObj, job, uintptr(pid)); ok == 0 {
		CloseHandle(job)
		return 0, false
	}
	return job, true
}

// TerminateJob 按 job 句柄终止整个 Job 里的进程树。
func TerminateJob(job uintptr, exitCode uint32) bool {
	r, _, _ := pcall(pTerminateJobObject, job, uintptr(exitCode))
	return r != 0
}
