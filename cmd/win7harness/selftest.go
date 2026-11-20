//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"win7harness/internal/bootstrap"
	"win7harness/internal/core"
	"win7harness/internal/selftest"
)

// runSelfTest 执行 -selftest：端到端跑一遍真实能力，产出可回传的报告。
//
// 退出码语义与 CI 对齐：0 全绿，1 有失败，2 自检未能开始。
// 报告默认落在 <数据目录>\selftest\reports\，文件名带时间戳，不会覆盖历史。
func runSelfTest(opt core.RunOptions) int {
	// 提前加载真实配置：Run 内部会把 WIN7HARNESS_HOME 指到沙箱，
	// 届时 core.Load() 读到的是默认配置。探测必须用用户的真实配置。
	realCfg := core.Load()
	opt2 := selftest.Options{
		// 故意不指定 DataDir：selftest 自己建沙箱并在结束时清理，
		// 不会把快照/日志/设备表写进真实数据目录。
		WorkDir:     opt.WorkDir,
		Timeout:     40 * time.Second,
		KeepSandbox: opt.SelfTestKeep,
	}

	if !opt.SelfTestOffline {
		opt2.ProviderTest = func() error {
			cfg := realCfg
			c, model, warn := bootstrap.NewClient(cfg)
			if warn != "" {
				core.Logf("自检：%s", warn)
			}
			if c == nil {
				return selftest.ErrNotConfigured
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			start := time.Now()
			if _, err := c.Ping(ctx); err != nil {
				return err
			}
			core.Logf("自检：模型 %s 连通，用时 %dms", model, time.Since(start).Milliseconds())
			return nil
		}
	}

	rep := selftest.Run(opt2)

	if opt.SelfTestJSON {
		fmt.Println(rep.JSON())
	} else {
		fmt.Print(rep.Text())
	}

	dir := opt.SelfTestOut
	if dir == "" {
		dir = selftest.DefaultReportDir()
	}
	md, js, err := rep.WriteReport(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n报告写入失败：%v\n", err)
	} else {
		fmt.Printf("\n报告已保存：\n  %s\n  %s\n", md, js)
		fmt.Println("把这两个文件回传给开发者，即可复现本机问题。")
	}
	if opt2.KeepSandbox && rep.WorkDir != "" {
		fmt.Printf("沙箱已保留：%s\n", rep.WorkDir)
	}
	return rep.ExitCode()
}
