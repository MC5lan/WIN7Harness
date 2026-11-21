# Win32 导出函数归属与启动自检

## 1. 起因

v1.29.0 在 Win11 上双击后立刻弹「程序遇到未预期的错误」：

```
Failed to find FillRect procedure in gdi32.dll: The specified procedure could not be found.
```

`FillRect` 不是 gdi32 的函数，它由 **user32.dll** 导出。`syscall.LazyProc` 是惰性解析的，
只有第一次 `Call` 才做 `GetProcAddress`，失败直接 panic —— 于是整程序在启动建窗后第一帧
落到底色填充时就死了，且提示里看不出是封装层写错了 DLL。

## 2. 本次修掉的三处

| 函数 | 原来挂在 | 实际所在 | 后果 |
| --- | --- | --- | --- |
| `FillRect` | gdi32.dll | **user32.dll** | 启动首帧填充即崩溃 |
| `DrawTextW` | gdi32.dll | **user32.dll** | 一旦走到文字绘制就崩溃 |
| `Get/SetWindowLongPtrW`、`SetClassLongPtrW` | 直接取 `...PtrW` | 32 位系统上是 `...LongW` | 386 包在 32 位 Win7 上启动即崩（设置窗口图标时触发） |

第三条是隐藏的：64 位 Windows 的 user32 导出 `GetWindowLongPtrW`，32 位只导出
`GetWindowLongW`。Go 的 `uintptr` 宽度随目标架构变化，所以必须按架构选名字：

```go
func longProc(a32, a64 string) *syscall.LazyProc {
	if unsafe.Sizeof(uintptr(0)) == 4 {
		return dUser32.NewProc(a32)
	}
	return dUser32.NewProc(a64)
}
```

## 3. 不再让一个 API 拖死整个程序

`internal/win32/proc_safe.go` 增加了安全调用层：

- 包内 140 处 `pX.Call(...)` 全部换成 `pcall(pX, ...)`：找不到导出函数时记入缺失清单、
  返回零值，功能降级但不 panic；
- 包外动态取函数的场景（`ui/image.go` 的 `StretchDIBits` / `SetStretchBltMode`）
  走 `win32.SafeCall`；
- 缺失记录同时通过 `OutputDebugString` 输出（DebugView 可见），并可用
  `win32.MissingProcs()` 取回。

## 4. 启动自检

`win32.SelfCheck()` 在 `main` 启动早期把 **118 个**已注册的导出函数逐个 `Find()`，
结果写进启动日志：

```
Win32 导出函数自检通过（118 个）
```

或

```
!! Win32 导出函数缺失 2 个：[FillRect DrawTextW]
```

这样以后出现「某个按钮点下去没反应」这类问题，翻日志第一屏就能定位，不必再靠猜。

## 5. 以后新增 API 的规矩

1. 名字挂哪个 DLL 一定去官方文档确认，不要凭印象；`FillRect`、`DrawTextW`、
   `SetWindowTextW` 这类"看起来像 GDI"的函数其实都在 user32。
2. 带 `Ptr` 的 `Long` 系列一律走 `longProc`，不要写死。
3. 新增的 proc 变量会自动被 `SelfCheck` 覆盖（它按变量声明扫描生成），
   不用额外登记。
4. 新调用一律用 `pcall`，不要直写 `.Call(`。

## 6. unsafe.Pointer 与 syscall

`GOOS=windows go vet ./...` 在 v1.38.1 上报告 35 处
`possible misuse of unsafe.Pointer`，全部集中在 `internal/win32` 与 `internal/ui`
的 Win32 / COM 互操作处。这些不是可以靠改代码消除的缺陷，而是 syscall 互操作的固有形态：

- **指针来源是操作系统，不是 Go 堆**：`HWND`、`WM_*` 的 `lParam`、`GlobalLock`
  返回的内存、COM 接口指针与调用方传入的 out 参数（`ppv`、`psz`、`pvar`），
  生命周期由 Windows 或调用方管理。Go 的 GC 不移动也不回收它们。
- **`unsafe.Pointer(uintptr)` 是唯一可行的转换**：Windows 返回的是整数句柄/地址，
  要按结构体或切片访问只能这样转。改成 `uintptr` 运算反而更危险。
- **真正的风险只有一种**：把 **Go 堆对象** 的 `uintptr(unsafe.Pointer(p))`
  存进变量，跨语句后再传给 syscall。Go 的规则要求转换与调用在同一表达式内完成，
  并在调用后 `runtime.KeepAlive`。

本轮按这个标准逐个核对了 35 处，并额外修掉一处未被 vet 捕获的真实风险：
`internal/win32/single.go` 的 `ActivateWindow` 曾把 `UTF16PtrFromString` 返回的
Go 堆指针先存成 `uintptr`，再调用 `FindWindowW`。现已改为调用点内联转换 +
`runtime.KeepAlive(cp/tp)`。

因此 CI 使用：

```bash
GOOS=windows go vet -unsafeptr=false ./...
```

`-unsafeptr=false` 只关闭“uintptr→unsafe.Pointer 可能无效”的启发式检查，
不会掩盖其它 vet 问题。不要用 `//nolint`（vet 不认），也不要把所有
`unsafe.Pointer` 批量替换成 `uintptr`。
