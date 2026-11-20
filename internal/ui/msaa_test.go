//go:build windows && amd64

package ui

import (
	"syscall"
	"testing"
	"unsafe"

	"win7harness/internal/win32"
)

var pSysFreeString = syscall.NewLazyDLL("oleaut32.dll").NewProc("SysFreeString")

func vcall(fn uintptr, args ...uintptr) uintptr {
	var a [8]uintptr
	copy(a[:], args)
	r, _, _ := syscall.Syscall(fn, uintptr(len(args)), a[0], a[1], a[2])
	return r
}

type fakeAccessibleApp struct{}

func (fakeAccessibleApp) AccessibleItems() []AccessibleItem {
	return []AccessibleItem{{Name: "对话记录", Value: "hello", Role: RoleSystemDocument, W: 100, H: 100}}
}

func TestMSAAObjectMarshals(t *testing.T) {
	win32.CoInit()
	defer win32.CoUninit()
	StopMSAAForTest()
	r, ok := lresultForApp(win32.HWND(0), 0, fakeAccessibleApp{})
	if !ok || r == 0 {
		t.Fatalf("LresultFromObject 未返回可编组对象：r=%d ok=%v", r, ok)
	}
	obj, ok := win32.ObjectFromLresult(r, 0)
	if !ok || obj == 0 {
		t.Fatalf("ObjectFromLresult 失败：obj=%d ok=%v", obj, ok)
	}
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := func(idx int) uintptr {
		return *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	}
	var count int32
	if hr := vcall(fn(8), obj, uintptr(unsafe.Pointer(&count)), 0); int32(hr) < 0 || count != 1 {
		t.Fatalf("accChildCount hr=%#x count=%d", hr, count)
	}
	child := msaaVariant{vt: msaaVtI4, val0: 1}
	var name uintptr
	if hr := vcall(fn(10), obj, uintptr(unsafe.Pointer(&child)), uintptr(unsafe.Pointer(&name))); int32(hr) < 0 || name == 0 {
		t.Fatalf("accName hr=%#x name=%d", hr, name)
	}
	got := syscall.UTF16ToString((*[1 << 16]uint16)(unsafe.Pointer(name))[:])
	pSysFreeString.Call(name)
	if got != "对话记录" {
		t.Fatalf("accName=%q", got)
	}
	var value uintptr
	child = msaaVariant{vt: msaaVtI4, val0: 1}
	if hr := vcall(fn(11), obj, uintptr(unsafe.Pointer(&child)), uintptr(unsafe.Pointer(&value))); int32(hr) < 0 || value == 0 {
		t.Fatalf("accValue hr=%#x value=%d", hr, value)
	}
	got = syscall.UTF16ToString((*[1 << 16]uint16)(unsafe.Pointer(value))[:])
	pSysFreeString.Call(value)
	if got != "hello" {
		t.Fatalf("accValue=%q", got)
	}
	vcall(fn(2), obj, 0, 0)
}

// StopMSAAForTest 只用于测试对象表不残留；生产无需调用。
func StopMSAAForTest() {
	msaaMu.Lock()
	msaaObjects = map[uintptr]*msaaObject{}
	msaaMu.Unlock()
}
