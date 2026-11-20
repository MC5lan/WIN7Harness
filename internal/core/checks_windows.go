//go:build windows

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	ntdll                    = syscall.NewLazyDLL("ntdll.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
	procDiskFreeSpaceExW     = kernel32.NewProc("GetDiskFreeSpaceExW")
	procRtlGetVersion        = ntdll.NewProc("RtlGetVersion")
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

type osVersionInfoExW struct {
	OSVersionInfoSize uint32
	MajorVersion      uint32
	MinorVersion      uint32
	BuildNumber       uint32
	PlatformID        uint32
	CSDVersion        [128]uint16
	ServicePackMajor  uint16
	ServicePackMinor  uint16
	SuiteMask         uint16
	ProductType       byte
	Reserved          byte
}

func platformChecks() []CheckResult {
	var out []CheckResult

	// 系统版本与 SP
	r := CheckResult{Name: "系统版本"}
	var vi osVersionInfoExW
	vi.OSVersionInfoSize = uint32(unsafe.Sizeof(vi))
	rc, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&vi)))
	if rc == 0 {
		sp := ""
		if vi.ServicePackMajor > 0 {
			sp = fmt.Sprintf(" SP%d", vi.ServicePackMajor)
		}
		r.Detail = fmt.Sprintf("Windows %d.%d 构建 %d%s（%s）",
			vi.MajorVersion, vi.MinorVersion, vi.BuildNumber, sp, osArchName())
		r.Status = CheckOK
		if vi.MajorVersion == 6 && vi.MinorVersion == 1 && vi.ServicePackMajor == 0 {
			r.Status = CheckWarn
			r.Hint = "Win7 未安装 SP1，TLS 与部分 API 可能异常，建议打 SP1 + KB2533623 + KB4474419"
		} else if vi.MajorVersion < 6 {
			r.Status = CheckFail
			r.Hint = "系统低于 Vista，程序无法运行"
		}
	} else {
		r.Status, r.Detail = CheckSkip, "无法读取系统版本"
	}
	out = append(out, r)

	// 内存
	mr := CheckResult{Name: "可用内存"}
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	ok, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if ok != 0 {
		mr.Detail = fmt.Sprintf("可用 %.0f MB / 共 %.0f MB（占用 %d%%）",
			float64(ms.AvailPhys)/1048576, float64(ms.TotalPhys)/1048576, ms.MemoryLoad)
		switch {
		case ms.AvailPhys < 200*1024*1024:
			mr.Status, mr.Hint = CheckFail, "内存过低，32 位进程可能分配失败，请关闭其它程序"
		case ms.AvailPhys < 500*1024*1024:
			mr.Status, mr.Hint = CheckWarn, "内存偏紧，处理大文件/长对话时可能卡顿"
		default:
			mr.Status = CheckOK
		}
		// 32 位进程地址空间上限约 2GB（大地址感知未开时）
		if isProcess32() && ms.TotalPhys > 4*1024*1024*1024 {
			mr.Detail += "；注意：当前为 32 位程序，最多只能用约 2GB"
		}
	} else {
		mr.Status, mr.Detail = CheckSkip, "无法读取内存信息"
	}
	out = append(out, mr)

	// 字体
	fr := CheckResult{Name: "界面字体"}
	windir := os.Getenv("SystemRoot")
	if windir == "" {
		windir = `C:\Windows`
	}
	missing := []string{}
	for _, f := range []string{"msyh.ttc", "consola.ttf"} {
		if _, err := os.Stat(filepath.Join(windir, "Fonts", f)); err != nil {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		fr.Status, fr.Detail = CheckOK, "Microsoft YaHei / Consolas 均存在"
	} else {
		fr.Status, fr.Detail = CheckWarn, "缺失："+joinComma(missing)
		fr.Hint = "界面会回退到系统默认字体，显示效果可能变差（Win7 精简版常见）"
	}
	out = append(out, fr)

	return out
}

// OSVersion 返回当前系统版本（Windows 为 Major.Minor.Build）。
// 工具链预检与支持包用它判断“当前是 Win7 还是 Win10/11”，
// 避免把 Win7 的上限错误地套到新系统上。
func OSVersion() string { return osVersion() }

func osVersion() string {
	var vi osVersionInfoExW
	vi.OSVersionInfoSize = uint32(unsafe.Sizeof(vi))
	if rc, _, _ := procRtlGetVersion.Call(uintptr(unsafe.Pointer(&vi))); rc == 0 {
		return fmt.Sprintf("Windows %d.%d.%d", vi.MajorVersion, vi.MinorVersion, vi.BuildNumber)
	}
	return "Windows"
}

func diskFree(dir string) (free, total uint64, err error) {
	ptr, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0, err
	}
	var freeUser, totalBytes, totalFree uint64
	ok, _, e := procDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&freeUser)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if ok == 0 {
		return 0, 0, e
	}
	return totalFree, totalBytes, nil
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += "、"
		}
		out += s
	}
	return out
}
