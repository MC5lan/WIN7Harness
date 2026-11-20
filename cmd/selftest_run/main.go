// selftest_run 是端到端自检的命令行入口：不依赖 Win32 界面，
// 直接跑 internal/selftest 的 77 项用例并打印汇总，供交叉编译前的回归使用。
// 用法：go run ./cmd/selftest_run <沙箱目录>
package main

import (
	"fmt"
	"os"
	"time"

	"win7harness/internal/selftest"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法：selftest_run <沙箱目录>")
		os.Exit(2)
	}
	rep := selftest.Run(selftest.Options{
		WorkDir: os.Args[1],
		Timeout: 60 * time.Second,
	})
	fmt.Print(rep.Text())
	fmt.Printf("\nPASS=%d FAIL=%d SKIP=%d TOTAL=%d\n",
		rep.Count(selftest.StatusPass), rep.Count(selftest.StatusFail),
		rep.Count(selftest.StatusSkip), rep.Total())
	if !rep.OK() {
		os.Exit(1)
	}
}
