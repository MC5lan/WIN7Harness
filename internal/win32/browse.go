//go:build windows

package win32

import (
	"syscall"
	"unsafe"
)

// 系统目录选择对话框。Win7 上 SHBrowseForFolder 比 IFileDialog 更稳：
// 不依赖 COM 版本，也不会因为缺少 Common Item Dialog 而打不开。
var (
	pSHBrowseForFolderW    = dShell32.NewProc("SHBrowseForFolderW")
	pSHGetPathFromIDListW  = dShell32.NewProc("SHGetPathFromIDListW")
	pCoTaskMemFree         = dOle32.NewProc("CoTaskMemFree")
	browseSetSelectionOnce uintptr
)

const (
	bifReturnOnlyFSDirs = 0x0001
	bifEditBox          = 0x0010
	bifNewDialogStyle   = 0x0040
	bffmInitialized     = 1
	bffmSetSelectionW   = 0x0467 // WM_USER + 103
)

// browseInfoW 对应 BROWSEINFOW。字段顺序与 Win32 一致，
// 64 位下 ulFlags 后由编译器自动补齐 4 字节对齐 lpfn。
type browseInfoW struct {
	hwndOwner      HWND
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

// browseInitDir 保留初始目录指针到回调结束，避免被 GC 提前回收。
var browseInitDir *uint16

func getModulePathDir() string { return "" }

// BrowseForFolder 弹出系统文件夹选择框，返回绝对路径；用户取消时 ok 为 false。
// initial 非空时尝试定位到该目录（由回调在初始化时发送 BFFM_SETSELECTION 实现）。
func BrowseForFolder(owner HWND, title, initial string) (string, bool) {
	t, _ := syscall.UTF16PtrFromString(title)
	if title == "" {
		t, _ = syscall.UTF16PtrFromString("选择工作区目录")
	}
	nameBuf := make([]uint16, 260)

	initDir := ""
	if initial != "" {
		initDir = initial
	}
	if initDir != "" {
		browseInitDir, _ = syscall.UTF16PtrFromString(initDir)
	} else {
		browseInitDir = nil
	}

	// 回调只做一件事：对话框初始化完成后把选中项定位到当前工作区。
	// NewCallback 只能创建一次，这里做惰性缓存。
	if browseSetSelectionOnce == 0 {
		browseSetSelectionOnce = syscall.NewCallback(func(hwnd, msg, lp, data uintptr) uintptr {
			if msg == bffmInitialized && browseInitDir != nil {
				// wParam=TRUE 表示 data 是绝对路径
				_, _, _ = pcall(pSendMessageW, hwnd, bffmSetSelectionW, 1, uintptr(unsafe.Pointer(browseInitDir)))
			}
			return 0
		})
	}

	bi := browseInfoW{
		hwndOwner:      owner,
		pszDisplayName: &nameBuf[0],
		lpszTitle:      t,
		ulFlags:        bifReturnOnlyFSDirs | bifEditBox | bifNewDialogStyle,
		lpfn:           browseSetSelectionOnce,
	}
	r, _, _ := pcall(pSHBrowseForFolderW, uintptr(unsafe.Pointer(&bi)))
	if r == 0 {
		return "", false
	}
	defer func() { _, _, _ = pcall(pCoTaskMemFree, r) }()

	pathBuf := make([]uint16, 260)
	ok, _, _ := pcall(pSHGetPathFromIDListW, r, uintptr(unsafe.Pointer(&pathBuf[0])))
	if ok == 0 {
		return "", false
	}
	return syscall.UTF16ToString(pathBuf), true
}
