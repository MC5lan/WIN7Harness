//go:build windows

package main

import (
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"runtime"
	"runtime/debug"

	"win7harness/internal/app"
	"win7harness/internal/core"
	"win7harness/internal/ui"
	"win7harness/internal/win32"
)

// buildTime 由构建脚本通过 -ldflags "-X main.buildTime=..." 注入，
// 留在启动日志里，方便用户报问题时确认拿的是哪个包。
var buildTime = "dev"

func main() {
	// -version 必须在 COM/GUI/日志初始化之前处理：它要能在无桌面会话里直接返回。
	if len(os.Args) == 2 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println(versionString())
		return
	}

	// 自定义绘制的窗口要响应 WM_GETOBJECT，oleacc 的 LresultFromObject
	// 依赖当前线程初始化 COM（STA）；ShellExecute 也走同一套。
	win32.CoInit()
	defer win32.CoUninit()

	// 命令行模式下不弹窗，异常直接写 stderr（无人值守时弹窗会一直卡着）。
	runMode := false
	// 崩溃兜底：把 panic 写入日志并弹窗，避免"闪退无提示"。
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("%v\n\n%s", r, string(debug.Stack()))
			core.Logf("PANIC: %s", msg)
			// 单独落一份报告：日志里翻栈太费劲，报告里连环境、脱敏配置、日志尾部一起带上
			if path, err := core.WriteCrashReport("panic", msg); err == nil {
				core.Logf("异常报告：%s", path)
			}
			if runMode {
				fmt.Fprintf(os.Stderr, "程序遇到未预期的错误：%v\n日志：%s\n", r, core.LogPath())
				os.Exit(1)
			}
			win32.MsgBox(0, "WIN7 Harness v"+core.Version+"  (build "+buildTime+")"+
				"\n\n程序遇到未预期的错误：\n"+fmt.Sprintf("%v", r)+
				"\n\n异常报告（含日志尾部、环境与脱敏配置）：\n"+core.CrashDir()+
				"\n\n重新打开后输入 /crash 可查看最近报告。",
				"WIN7 Harness", win32.MB_ICONERROR|win32.MB_OK)
		}
		core.CloseLog()
	}()

	core.InitLog()
	// 老版本把会话/日志放在漫游 %APPDATA%；先迁移，再判断上次异常退出。
	core.MigrateLocalData()
	// 崩溃可观测：启动最早处判断上次是否异常退出（必须在 MarkRunning 之前），
	// 再轮转日志、清理过老的异常报告，避免长期运行把磁盘塞满。
	if hint, _ := core.AbnormalExitHint(); hint != "" {
		core.SetStartupWarning(hint)
		core.Logf("检测到上次异常退出")
	}
	core.RotateLogIfBig(2 << 20)
	core.PruneCrashReports(10)
	core.MarkRunning(core.Version)
	core.Logf("==== WIN7 Harness v%s 启动 (%s/%s, build %s) ====", core.Version, osName(), archName(), buildTime)
	if lim := core.ApplyMemoryPolicy(); lim > 0 {
		core.Logf("Go 堆软上限: %s", core.HumanSize(lim))
	} else {
		core.Logf("Go 堆软上限: 由 GOMEMLIMIT 环境变量控制")
	}
	if core.UpdatePublicKey != "" {
		core.Logf("更新清单验签: 已启用公钥")
	} else {
		core.Logf("更新清单验签: 未配置公钥（开发构建）")
	}
	// Win32 导出函数自检：名字写错 DLL、或某个 API 在当前系统根本不存在时，
	// 之前要等用户点到那个功能才 panic（表现为"打开就报错"或"点按钮没反应"）。
	// 启动时一次性解析并写进日志，缺什么直接可查。
	if bad := win32.SelfCheck(); len(bad) > 0 {
		core.Logf("!! Win32 导出函数缺失 %d 个：%v", len(bad), bad)
	} else {
		core.Logf("Win32 导出函数自检通过（%d 个）", win32.ProcCount())
	}

	// 命令行模式：-run "任务" 跑完即退，供脚本 / 批处理 / CI 调用。
	// 不占用单实例互斥量，也不初始化界面与字体。
	if opt, ok := core.ParseRunArgs(os.Args[1:]); ok {
		runMode = true
		cleanup := setupRunConsole()
		if msg := opt.Validate(); msg != "" {
			fmt.Fprintln(os.Stderr, msg)
			cleanup()
			core.MarkCleanExit(2)
			core.CloseLog()
			os.Exit(2)
		}
		code := 0
		if opt.Eval {
			// -eval 自己负责报告落盘；这里兜住退出码，
			// 否则会落到下面的 runTask 分支把 0/1/2 覆盖成 2。
			code = runEval(opt)
			cleanup()
			core.Logf("==== 评测结束，code=%d ====", code)
			core.MarkCleanExit(code)
			core.CloseLog()
			os.Exit(code)
		}
		if opt.SelfTest {
			code = runSelfTest(opt)
			cleanup()
			core.Logf("==== 自检结束，code=%d ====", code)
			core.MarkCleanExit(code)
			core.CloseLog()
			os.Exit(code)
		}
		code = runTask(opt)
		cleanup()
		core.Logf("==== 命令行任务结束，code=%d ====", code)
		// 正常结束也要写干净退出标记，否则下次开界面会误报"上次异常退出"
		core.MarkCleanExit(code)
		core.CloseLog()
		os.Exit(code)
	}

	// 单实例：同名互斥量已存在说明有实例在跑，激活它并退出，
	// 避免两个进程同时写 config.json 和会话文件。
	mutex, already := win32.AcquireSingleInstance(singleMutexName())
	if already {
		core.Logf("检测到已有实例，激活后退出")
		if win32.ActivateWindow(win32.ClassName, "") {
			win32.MsgBox(0, "WIN7 Harness 已经在运行了，已为你切到那个窗口。\n\n如果没看到窗口，请在任务栏里找一下。",
				"WIN7 Harness", win32.MB_ICONINFORMATION|win32.MB_OK)
		} else {
			win32.MsgBox(0, "WIN7 Harness 已经在运行，但没能找到它的窗口。\n请在任务栏里手动切过去。",
				"WIN7 Harness", win32.MB_ICONINFORMATION|win32.MB_OK)
		}
		return
	}
	defer win32.CloseHandle(mutex)

	cfg := core.Load()
	// 配置里的兼容渲染开关只在环境变量未显式设置时生效：
	// 排障时 WIN7H_RENDER 仍可临时覆盖配置，不必改文件。
	if _, ok := os.LookupEnv("WIN7H_RENDER"); !ok && cfg.RenderMode == "gdi" {
		_ = os.Setenv("WIN7H_RENDER", "gdi")
	}
	if err := core.EnsureDirs(cfg); err != nil {
		core.Logf("创建目录失败: %v", err)
	}

	a := app.New(cfg, nil)
	ui.InitFonts()
	code := ui.RunWindow("WIN7 Harness", 1280, 860, a)
	core.Logf("==== 退出，code=%d ====", code)
	core.MarkCleanExit(code)
	os.Exit(code)
}

// versionString 是 -version 的唯一输出格式，便于测试与脚本解析。
func versionString() string {
	return fmt.Sprintf("WIN7 Harness v%s (%s/%s, build %s)", core.Version, runtime.GOOS, runtime.GOARCH, buildTime)
}

// singleMutexName 用数据目录区分实例：便携版放在不同目录时可各开一个。
func singleMutexName() string {
	h := fnv.New64a()
	io.WriteString(h, core.DataDir())
	return fmt.Sprintf("Local\\Win7Harness-%x", h.Sum64())
}

func osName() string { return "windows" }

func archName() string {
	if core.Is386() {
		return "386"
	}
	return "amd64"
}
