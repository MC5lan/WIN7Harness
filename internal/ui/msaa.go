//go:build windows

package ui

import (
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"win7harness/internal/win32"
)

// MSAA 角色/状态常量（oleacc.h）。
const (
	RoleSystemClient     = 10
	RoleSystemDocument   = 15
	RoleSystemText       = 42
	RoleSystemPushButton = 43

	StateSystemFocused   = 0x00000004
	StateSystemFocusable = 0x00100000
	StateSystemReadOnly  = 0x00000040
)

const (
	msaaSOK          = 0
	msaaSFalse       = 1
	msaaEPointer     = 0x80004003
	msaaEInvalidArg  = 0x80070057
	msaaENotImpl     = 0x80004001
	msaaENoInterface = 0x80004002
	msaaEOutOfMemory = 0x8007000E
)

const (
	msaaVtEmpty = 0
	msaaVtI4    = 3
	msaaVtBStr  = 8
)

// AccessibleItem 是一个语义项（对话正文、输入框、按钮等）。
// 位置是客户区坐标；文本在 WM_GETOBJECT 时做快照，避免 COM 线程
// 直接读 App 的可变状态。
type AccessibleItem struct {
	Name          string
	Value         string
	Description   string
	DefaultAction string
	Role          uint32
	State         uint32
	X, Y          int32
	W, H          int32
	Focused       bool
}

// AccessibleApp 是可选能力：实现后 WM_GETOBJECT 返回自定义虚拟 IAccessible树。
type AccessibleApp interface {
	AccessibleItems() []AccessibleItem
}

type msaaObject struct {
	vtbl  *[28]uintptr
	refs  int32
	hwnd  win32.HWND
	items []AccessibleItem
}

var (
	msaaMu      sync.Mutex
	msaaObjects = map[uintptr]*msaaObject{}
	msaaVtable  [28]uintptr
	pSysAlloc   = syscall.NewLazyDLL("oleaut32.dll").NewProc("SysAllocString")
)

func newMSAAObject(hwnd win32.HWND, items []AccessibleItem) *msaaObject {
	obj := &msaaObject{vtbl: &msaaVtable, refs: 1, hwnd: hwnd}
	if len(items) > 0 {
		obj.items = append([]AccessibleItem(nil), items...)
	}
	msaaMu.Lock()
	msaaObjects[uintptr(unsafe.Pointer(obj))] = obj
	msaaMu.Unlock()
	return obj
}

func msaaLookup(this uintptr) *msaaObject {
	msaaMu.Lock()
	o := msaaObjects[this]
	msaaMu.Unlock()
	return o
}

func (o *msaaObject) addRef() int32 {
	if o == nil {
		return 0
	}
	return atomic.AddInt32(&o.refs, 1)
}

func (o *msaaObject) release() int32 {
	if o == nil {
		return 0
	}
	n := atomic.AddInt32(&o.refs, -1)
	if n <= 0 {
		msaaMu.Lock()
		delete(msaaObjects, uintptr(unsafe.Pointer(o)))
		msaaMu.Unlock()
		return 0
	}
	return n
}

func msaaChild(p uintptr) int32 {
	if p == 0 {
		return 0
	}
	v := (*msaaVariant)(unsafe.Pointer(p))
	if v.vt == msaaVtI4 {
		return int32(v.val0)
	}
	return 0
}

func msaaSetI4(p uintptr, v int32) {
	if p == 0 {
		return
	}
	*(*msaaVariant)(unsafe.Pointer(p)) = msaaVariant{vt: msaaVtI4, val0: uintptr(uint32(v))}
}

func msaaSetEmpty(p uintptr) {
	if p == 0 {
		return
	}
	*(*msaaVariant)(unsafe.Pointer(p)) = msaaVariant{}
}

type msaaVariant struct {
	vt   uint16
	r1   uint16
	r2   uint16
	r3   uint16
	val0 uintptr
	val1 uintptr
}

func msaaSetBSTR(out uintptr, s string) uintptr {
	if out == 0 {
		return msaaEPointer
	}
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return msaaEOutOfMemory
	}
	r, _, _ := pSysAlloc.Call(uintptr(unsafe.Pointer(p)))
	*(*uintptr)(unsafe.Pointer(out)) = r
	if r == 0 && s != "" {
		return msaaEOutOfMemory
	}
	return msaaSOK
}

func (o *msaaObject) childCount() int32 {
	if o == nil {
		return 0
	}
	return int32(len(o.items))
}

func (o *msaaObject) item(child int32) (*AccessibleItem, bool) {
	if o == nil || child <= 0 || int(child) > len(o.items) {
		return nil, false
	}
	return &o.items[child-1], true
}

func (o *msaaObject) screenRect(child int32) (int32, int32, int32, int32, bool) {
	if o == nil || o.hwnd == 0 {
		return 0, 0, 0, 0, false
	}
	pt := win32.POINT{}
	win32.ClientToScreenPoint(o.hwnd, &pt)
	if child == 0 {
		r := win32.ClientRect(o.hwnd)
		return pt.X, pt.Y, r.Width(), r.Height(), true
	}
	it, ok := o.item(child)
	if !ok {
		return 0, 0, 0, 0, false
	}
	return pt.X + it.X, pt.Y + it.Y, it.W, it.H, true
}

func (o *msaaObject) hitTest(x, y int32) int32 {
	if o == nil {
		return 0
	}
	pt := win32.POINT{X: x, Y: y}
	win32.ScreenToClientPoint(o.hwnd, &pt)
	// 反向遍历：对话项覆盖整窗，输入框/按钮在它上面，必须先命中后者。
	for i := len(o.items) - 1; i >= 0; i-- {
		it := &o.items[i]
		if pt.X >= it.X && pt.Y >= it.Y && pt.X < it.X+it.W && pt.Y < it.Y+it.H {
			return int32(i + 1)
		}
	}
	return 0
}

func (o *msaaObject) postClick(child int32) bool {
	it, ok := o.item(child)
	if !ok || o.hwnd == 0 {
		return false
	}
	x := it.X + it.W/2
	y := it.Y + it.H/2
	lp := uintptr(uint16(x)) | uintptr(uint16(y))<<16
	win32.PostMessage(o.hwnd, 0x0201, 1, lp) // WM_LBUTTONDOWN
	win32.PostMessage(o.hwnd, 0x0202, 0, lp) // WM_LBUTTONUP
	return true
}

func (o *msaaObject) focusedChild() int32 {
	for i := range o.items {
		if o.items[i].Focused {
			return int32(i + 1)
		}
	}
	return 0
}

func msaaName(o *msaaObject, child int32) string {
	if child == 0 {
		return "WIN7 Harness"
	}
	if it, ok := o.item(child); ok {
		return it.Name
	}
	return ""
}

func msaaValue(o *msaaObject, child int32) string {
	if child == 0 {
		return ""
	}
	if it, ok := o.item(child); ok {
		return it.Value
	}
	return ""
}

func msaaRole(o *msaaObject, child int32) uint32 {
	if child == 0 {
		return RoleSystemClient
	}
	if it, ok := o.item(child); ok {
		return it.Role
	}
	return 0
}

func msaaState(o *msaaObject, child int32) uint32 {
	if child == 0 {
		return StateSystemFocusable
	}
	if it, ok := o.item(child); ok {
		return it.State
	}
	return 0
}
