//go:build windows

package win32

// 多模态输入所需的系统能力：剪贴板位图、文件拖放、选图对话框。
// 全部走 Win32 原生 API，零第三方依赖，Win7 可用。

import (
	"syscall"
	"unsafe"
)

var (
	pGlobalSize    = dKernel32.NewProc("GlobalSize")
	pDragAccept    = dShell32.NewProc("DragAcceptFiles")
	pDragQueryFile = dShell32.NewProc("DragQueryFileW")
	pDragFinish    = dShell32.NewProc("DragFinish")
	pGetOpenFile   = dComdlg32.NewProc("GetOpenFileNameW")
)

const (
	CF_DIB   = 8
	CF_DIBV5 = 17

	WM_DROPFILES = 0x0233

	// OFN_* 打开文件对话框选项
	OFN_FILEMUSTEXIST    = 0x00001000
	OFN_PATHMUSTEXIST    = 0x00000800
	OFN_ALLOWMULTISELECT = 0x00000200
	OFN_EXPLORER         = 0x00080000
	OFN_NOCHANGEDIR      = 0x00000008
	OFN_HIDEREADONLY     = 0x00000004

	MAX_PATH_LONG = 32768
)

// openFileNameW 对应 Win32 的 OPENFILENAMEW 结构。
type openFileNameW struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	Flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uintptr
	FlagsEx           uint32
}

// Gdi32Proc 供界面层按需获取 gdi32 导出函数（如 StretchDIBits 贴图）。
func Gdi32Proc(name string) *syscall.LazyProc { return dGdi32.NewProc(name) }

// User32Proc 供界面层按需获取 user32 导出函数。
func User32Proc(name string) *syscall.LazyProc { return dUser32.NewProc(name) }

// GetClipboardDIB 读取剪贴板里的位图，返回裸 DIB 字节（以 BITMAPINFOHEADER 起始）。
// 优先取 CF_DIBV5（带完整 alpha），退回 CF_DIB。
func GetClipboardDIB() ([]byte, bool) {
	if r, _, _ := pcall(pOpenClipboard, 0); r == 0 {
		// 剪贴板被别的进程占用时直接放弃，不阻塞 UI
		return nil, false
	}
	defer pcall(pCloseClipboard)
	return readDIB()
}

func readDIB() ([]byte, bool) {
	for _, fmtID := range []uintptr{CF_DIBV5, CF_DIB} {
		h, _, _ := pcall(pGetClipboardData, fmtID)
		if h == 0 {
			continue
		}
		sz, _, _ := pcall(pGlobalSize, h)
		if sz == 0 || sz > 64<<20 {
			continue
		}
		lp, _, _ := pcall(pGlobalLock, h)
		if lp == 0 {
			continue
		}
		buf := make([]byte, int(sz))
		copy(buf, unsafe.Slice((*byte)(unsafe.Pointer(lp)), int(sz)))
		pcall(pGlobalUnlock, h)
		if len(buf) < 40 {
			continue
		}
		return buf, true
	}
	return nil, false
}

// HasClipboardImage 判断剪贴板里是否有位图。
func HasClipboardImage() bool {
	if r, _, _ := pcall(pOpenClipboard, 0); r == 0 {
		return false
	}
	defer pcall(pCloseClipboard)
	if h, _, _ := pcall(pGetClipboardData, CF_DIBV5); h != 0 {
		return true
	}
	h, _, _ := pcall(pGetClipboardData, CF_DIB)
	return h != 0
}

// EnableDropFiles 开启窗口的文件拖放接收（消息为 WM_DROPFILES）。
func EnableDropFiles(hwnd HWND) {
	pcall(pDragAccept, uintptr(hwnd), 1)
}

// ExtractDroppedFiles 从 WM_DROPFILES 的 wParam 中取出拖入的文件路径。
// 调用后内部会完成 DragFinish。
func ExtractDroppedFiles(hDrop uintptr) []string {
	if hDrop == 0 {
		return nil
	}
	defer pcall(pDragFinish, hDrop)
	n, _, _ := pcall(pDragQueryFile, hDrop, 0xFFFFFFFF, 0, 0)
	if n == 0 || n > 64 {
		return nil
	}
	out := make([]string, 0, n)
	buf := make([]uint16, 1024)
	for i := uintptr(0); i < n; i++ {
		r, _, _ := pcall(pDragQueryFile, hDrop, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if r == 0 {
			continue
		}
		out = append(out, syscall.UTF16ToString(buf[:r]))
	}
	return out
}

// openFileFilter 构造 OPENFILENAMEW 需要的多字符串过滤器：
// "显示名\0模式\0显示名\0模式\0\0"。
// syscall.StringToUTF16 遇到含 NUL 的字符串会直接 panic，
// 所以逐段转换（每段自带结尾 0）再拼接，最后补一个 0 形成双 NUL 终止。
func openFileFilter(parts ...string) []uint16 {
	var out []uint16
	for _, s := range parts {
		out = append(out, syscall.StringToUTF16(s)...)
	}
	return append(out, 0)
}

// OpenImageDialog 弹出系统选图对话框，返回选中的文件路径（可多选）。
// 用户取消返回空切片。
func OpenImageDialog(hwnd HWND, title string) []string {
	const bufSize = 32768
	buf := make([]uint16, bufSize) // 首段为目录，后续为文件名（多选时）
	filter := openFileFilter(
		"图片文件 (*.png;*.jpg;*.jpeg;*.gif;*.bmp)",
		"*.png;*.jpg;*.jpeg;*.gif;*.bmp",
		"所有文件 (*.*)",
		"*.*",
	)
	t := syscall.StringToUTF16(title)

	ofn := openFileNameW{
		hwndOwner:    uintptr(hwnd),
		lpstrFilter:  &filter[0],
		nFilterIndex: 1,
		lpstrFile:    &buf[0],
		nMaxFile:     bufSize,
		lpstrTitle:   &t[0],
		Flags:        OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST | OFN_ALLOWMULTISELECT | OFN_EXPLORER | OFN_NOCHANGEDIR | OFN_HIDEREADONLY,
	}
	ofn.lStructSize = uint32(unsafe.Sizeof(ofn))
	r, _, _ := pcall(pGetOpenFile, uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return nil
	}

	// 单选用 NUL 结尾；多选为 "目录\0文件1\0文件2\0\0"
	first := syscall.UTF16ToString(buf)
	// 找到第一个 NUL 的位置
	firstEnd := 0
	for firstEnd < len(buf) && buf[firstEnd] != 0 {
		firstEnd++
	}
	rest := buf[firstEnd+1:]
	if len(rest) == 0 || rest[0] == 0 {
		return []string{first}
	}
	dir := first
	var out []string
	i := 0
	for i < len(rest) && rest[i] != 0 {
		j := i
		for j < len(rest) && rest[j] != 0 {
			j++
		}
		name := syscall.UTF16ToString(rest[i:j])
		if name != "" {
			out = append(out, joinPath(dir, name))
		}
		i = j + 1
	}
	if len(out) == 0 {
		return []string{first}
	}
	return out
}

// OpenCertDialog 选择证书文件（.pem/.crt/.cer），返回单个路径。
func OpenCertDialog(hwnd HWND, title string) string {
	const bufSize = 4096
	buf := make([]uint16, bufSize)
	filter := openFileFilter(
		"证书文件 (*.pem;*.crt;*.cer)",
		"*.pem;*.crt;*.cer",
		"所有文件 (*.*)",
		"*.*",
	)
	t := syscall.StringToUTF16(title)
	ofn := openFileNameW{
		hwndOwner:    uintptr(hwnd),
		lpstrFilter:  &filter[0],
		nFilterIndex: 1,
		lpstrFile:    &buf[0],
		nMaxFile:     bufSize,
		lpstrTitle:   &t[0],
		Flags:        OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST | OFN_EXPLORER | OFN_NOCHANGEDIR | OFN_HIDEREADONLY,
	}
	ofn.lStructSize = uint32(unsafe.Sizeof(ofn))
	r, _, _ := pcall(pGetOpenFile, uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// joinPath 手写拼接，避免引入 path/filepath 的平台差异。
func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	if len(dir) > 0 && (dir[len(dir)-1] == '\\' || dir[len(dir)-1] == '/') {
		return dir + name
	}
	return dir + "\\" + name
}
