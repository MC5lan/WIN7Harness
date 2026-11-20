package core

import (
	"fmt"
	"runtime/debug"
)

// SafeGo 启动一个带 panic 兜底的 goroutine。
//
// 为什么需要它：任何未捕获的 panic（无论哪个 goroutine）都会直接杀死进程，
// 而 GUI 子系统没有控制台、main() 的 recover 兜不住——用户看到的就是"闪退"，
// 连崩溃报告都不会留。SafeGo 把 panic 记成崩溃报告 + 日志（定位到具体 goroutine），
// 避免一次后台任务崩溃直接带走整个程序。
//
// 注意：recover 不能"修复"现场。崩溃后如果涉及引擎/会话状态，程序可能处于
// 不一致状态；这里的选择是"尽量活着 + 一定要有证据"，而不是假装一切如常。
func SafeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				Logf("PANIC in goroutine %s: %v\n%s", name, r, stack)
				_, _ = WriteCrashReport("goroutine-panic",
					fmt.Sprintf("goroutine: %s\npanic: %v\n\n%s", name, r, stack))
			}
		}()
		fn()
	}()
}
