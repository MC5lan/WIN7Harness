//go:build windows && amd64

package ui

import (
	"syscall"
	"unsafe"

	"win7harness/internal/win32"
)

var msaaSupported = true

type msaaGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	msaaIIDUnknown    = msaaGUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	msaaIIDDispatch   = msaaGUID{Data1: 0x00020400, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	msaaIIDAccessible = msaaGUID{Data1: 0x618736E0, Data2: 0x3C3D, Data3: 0x11CF, Data4: [8]byte{0x81, 0x0C, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71}}
)

func guidEqual(a, b *msaaGUID) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Data1 == b.Data1 && a.Data2 == b.Data2 && a.Data3 == b.Data3 && a.Data4 == b.Data4
}

func msaaQueryInterface(this, riid, ppv uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil || riid == 0 || ppv == 0 {
		return msaaEPointer
	}
	g := (*msaaGUID)(unsafe.Pointer(riid))
	if !guidEqual(g, &msaaIIDUnknown) && !guidEqual(g, &msaaIIDDispatch) && !guidEqual(g, &msaaIIDAccessible) {
		*(*uintptr)(unsafe.Pointer(ppv)) = 0
		return msaaENoInterface
	}
	o.addRef()
	*(*uintptr)(unsafe.Pointer(ppv)) = this
	return msaaSOK
}

func msaaAddRef(this uintptr) uintptr {
	if o := msaaLookup(this); o != nil {
		return uintptr(o.addRef())
	}
	return 0
}

func msaaRelease(this uintptr) uintptr {
	if o := msaaLookup(this); o != nil {
		return uintptr(o.release())
	}
	return 0
}

func msaaGetTypeInfoCount(_ uintptr, pctinfo uintptr) uintptr {
	if pctinfo != 0 {
		*(*uint32)(unsafe.Pointer(pctinfo)) = 0
	}
	return msaaSOK
}

func msaaGetTypeInfo(_, _, _, _ uintptr) uintptr           { return msaaENotImpl }
func msaaGetIDsOfNames(_, _, _, _, _, _ uintptr) uintptr   { return msaaENotImpl }
func msaaInvoke(_, _, _, _, _, _, _, _, _ uintptr) uintptr { return msaaENotImpl }

func msaaGetAccParent(_ uintptr, ppdisp uintptr) uintptr {
	if ppdisp != 0 {
		*(*uintptr)(unsafe.Pointer(ppdisp)) = 0
	}
	return msaaSFalse
}

func msaaGetAccChildCount(this, pcount uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil || pcount == 0 {
		return msaaEPointer
	}
	*(*int32)(unsafe.Pointer(pcount)) = o.childCount()
	return msaaSOK
}

func msaaGetAccChild(_ uintptr, _ uintptr, ppdisp uintptr) uintptr {
	if ppdisp != 0 {
		*(*uintptr)(unsafe.Pointer(ppdisp)) = 0
	}
	return msaaSFalse
}

func msaaGetAccName(this, varChild, psz uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil {
		return msaaEPointer
	}
	child := msaaChild(varChild)
	if child < 0 || int(child) > len(o.items) {
		return msaaEInvalidArg
	}
	return msaaSetBSTR(psz, msaaName(o, child))
}

func msaaGetAccValue(this, varChild, psz uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil {
		return msaaEPointer
	}
	child := msaaChild(varChild)
	if child < 0 || int(child) > len(o.items) {
		return msaaEInvalidArg
	}
	return msaaSetBSTR(psz, msaaValue(o, child))
}

func msaaGetAccDescription(this, varChild, psz uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil {
		return msaaEPointer
	}
	child := msaaChild(varChild)
	if child == 0 {
		return msaaSetBSTR(psz, "WIN7 Harness 聊天与会话界面")
	}
	if it, ok := o.item(child); ok {
		return msaaSetBSTR(psz, it.Description)
	}
	return msaaEInvalidArg
}

func msaaGetAccRole(this, varChild, pvar uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil {
		return msaaEPointer
	}
	child := msaaChild(varChild)
	if child < 0 || int(child) > len(o.items) {
		return msaaEInvalidArg
	}
	msaaSetI4(pvar, int32(msaaRole(o, child)))
	return msaaSOK
}

func msaaGetAccState(this, varChild, pvar uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil {
		return msaaEPointer
	}
	child := msaaChild(varChild)
	if child < 0 || int(child) > len(o.items) {
		return msaaEInvalidArg
	}
	msaaSetI4(pvar, int32(msaaState(o, child)))
	return msaaSOK
}

func msaaGetAccHelp(_ uintptr, _ uintptr, psz uintptr) uintptr {
	return msaaSetBSTR(psz, "")
}

func msaaGetAccHelpTopic(_ uintptr, pszHelpFile, _ uintptr, pidTopic uintptr) uintptr {
	if pszHelpFile != 0 {
		*(*uintptr)(unsafe.Pointer(pszHelpFile)) = 0
	}
	if pidTopic != 0 {
		*(*int32)(unsafe.Pointer(pidTopic)) = 0
	}
	return msaaSFalse
}

func msaaGetAccKeyboardShortcut(_ uintptr, _ uintptr, psz uintptr) uintptr {
	return msaaSetBSTR(psz, "")
}

func msaaGetAccFocus(this, pvar uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil {
		return msaaEPointer
	}
	msaaSetI4(pvar, o.focusedChild())
	return msaaSOK
}

func msaaGetAccSelection(this, pvar uintptr) uintptr {
	return msaaGetAccFocus(this, pvar)
}

func msaaGetAccDefaultAction(this, varChild, psz uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil {
		return msaaEPointer
	}
	child := msaaChild(varChild)
	if it, ok := o.item(child); ok {
		return msaaSetBSTR(psz, it.DefaultAction)
	}
	return msaaSetBSTR(psz, "")
}

func msaaAccSelect(this, flags, varChild uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil {
		return msaaEPointer
	}
	// SELFLAG_TAKEFOCUS / TAKESELECTION：投递一次点击，让输入框等
	// 自定义控件真正进入焦点，而不是只对屏幕阅读器假装成功。
	if flags&3 != 0 {
		if it, ok := o.item(msaaChild(varChild)); ok && it.Role != RoleSystemPushButton {
			// 输入框/正文选择焦点用点击；按钮的 accSelect 只取焦点，
			// 真正按下走 accDoDefaultAction，避免辅助工具误发送。
			o.postClick(msaaChild(varChild))
		}
	}
	return msaaSOK
}

func msaaAccLocation(this, px, py, pcx, pcy, varChild uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil {
		return msaaEPointer
	}
	x, y, w, h, ok := o.screenRect(msaaChild(varChild))
	if !ok {
		return msaaEInvalidArg
	}
	if px != 0 {
		*(*int32)(unsafe.Pointer(px)) = x
	}
	if py != 0 {
		*(*int32)(unsafe.Pointer(py)) = y
	}
	if pcx != 0 {
		*(*int32)(unsafe.Pointer(pcx)) = w
	}
	if pcy != 0 {
		*(*int32)(unsafe.Pointer(pcy)) = h
	}
	return msaaSOK
}

func msaaAccNavigate(this, navDir, varStart, pvarEnd uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil {
		return msaaEPointer
	}
	start := msaaChild(varStart)
	end := int32(0)
	switch navDir {
	case 5: // NAVDIR_NEXT
		if start > 0 && int(start) < len(o.items) {
			end = start + 1
		}
	case 6: // NAVDIR_PREVIOUS
		if start > 1 {
			end = start - 1
		}
	case 7: // NAVDIR_FIRSTCHILD
		if len(o.items) > 0 {
			end = 1
		}
	case 8: // NAVDIR_LASTCHILD
		end = int32(len(o.items))
	}
	if end == 0 {
		msaaSetEmpty(pvarEnd)
		return msaaSFalse
	}
	msaaSetI4(pvarEnd, end)
	return msaaSOK
}

func msaaAccHitTest(this, x, y, pvar uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil {
		return msaaEPointer
	}
	msaaSetI4(pvar, o.hitTest(int32(x), int32(y)))
	return msaaSOK
}

func msaaAccDoDefaultAction(this, varChild uintptr) uintptr {
	o := msaaLookup(this)
	if o == nil {
		return msaaEPointer
	}
	it, ok := o.item(msaaChild(varChild))
	if !ok || it.DefaultAction == "" {
		return msaaENotImpl
	}
	// 自定义绘制的按钮没有 HWND/BM_CLICK；按语义项中心投递鼠标消息，
	// App 的 UI 命中测试会走和真实点击完全相同的路径。
	o.postClick(msaaChild(varChild))
	return msaaSOK
}

func msaaPutAccName(_, _, _ uintptr) uintptr  { return msaaENotImpl }
func msaaPutAccValue(_, _, _ uintptr) uintptr { return msaaENotImpl }

func init() {
	msaaVtable = [28]uintptr{
		syscall.NewCallback(msaaQueryInterface),
		syscall.NewCallback(msaaAddRef),
		syscall.NewCallback(msaaRelease),
		syscall.NewCallback(msaaGetTypeInfoCount),
		syscall.NewCallback(msaaGetTypeInfo),
		syscall.NewCallback(msaaGetIDsOfNames),
		syscall.NewCallback(msaaInvoke),
		syscall.NewCallback(msaaGetAccParent),
		syscall.NewCallback(msaaGetAccChildCount),
		syscall.NewCallback(msaaGetAccChild),
		syscall.NewCallback(msaaGetAccName),
		syscall.NewCallback(msaaGetAccValue),
		syscall.NewCallback(msaaGetAccDescription),
		syscall.NewCallback(msaaGetAccRole),
		syscall.NewCallback(msaaGetAccState),
		syscall.NewCallback(msaaGetAccHelp),
		syscall.NewCallback(msaaGetAccHelpTopic),
		syscall.NewCallback(msaaGetAccKeyboardShortcut),
		syscall.NewCallback(msaaGetAccFocus),
		syscall.NewCallback(msaaGetAccSelection),
		syscall.NewCallback(msaaGetAccDefaultAction),
		syscall.NewCallback(msaaAccSelect),
		syscall.NewCallback(msaaAccLocation),
		syscall.NewCallback(msaaAccNavigate),
		syscall.NewCallback(msaaAccHitTest),
		syscall.NewCallback(msaaAccDoDefaultAction),
		syscall.NewCallback(msaaPutAccName),
		syscall.NewCallback(msaaPutAccValue),
	}
}

// lresultForApp 在 UI 线程做快照并交给 OLE 编组。
func lresultForApp(hwnd win32.HWND, wParam uintptr, app AccessibleApp) (uintptr, bool) {
	if !msaaSupported || app == nil {
		return 0, false
	}
	obj := newMSAAObject(hwnd, app.AccessibleItems())
	r, ok := win32.LresultForObject(uintptr(unsafe.Pointer(obj)), wParam)
	obj.release()
	return r, ok
}
