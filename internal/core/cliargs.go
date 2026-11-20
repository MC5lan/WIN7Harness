package core

import "strings"

// RunOptions 是命令行模式（-run / -script）的解析结果。
// 放在 core 里而不是 cmd 里，是为了能跨平台写单元测试：
// cmd 下所有文件都带 windows 构建标签，测不到。
type RunOptions struct {
	Prompt  string
	WorkDir string
	Yes     bool   // 放行需要确认的写操作
	Plan    bool   // 只读模式
	Quiet   bool   // 只输出最终答复
	Timeout int    // 秒，单任务超时
	Format  string // text | json

	// Script 任务清单文件路径（-script）。一行一个任务，交给 -run 的同一套引擎串行跑。
	Script string
	// KeepGoing 单个任务失败后继续跑后面的任务（-keep-going）。默认遇错即停。
	KeepGoing bool
	// Shared 所有任务共用同一段上下文（-shared）。默认每个任务互相隔离。
	Shared bool

	// SelfTest 运行端到端自检（-selftest）后退出，不进入界面。
	// 这是 v1.19 的"真机验证闭环"入口：任意一台 Win7 跑一次，产出可回传的报告。
	SelfTest bool
	// SelfTestOut 自检报告输出目录（-out），默认数据目录下 selftest/reports。
	SelfTestOut string
	// SelfTestJSON 只向 stdout 输出 JSON 报告（-selftest --json）。
	SelfTestJSON bool
	// SelfTestOffline 跳过模型连通性探测（--offline），无网络环境下用。
	SelfTestOffline bool
	// SelfTestKeep 保留自检沙箱目录（--keep），便于失败后现场取证。
	SelfTestKeep bool
	// Eval 运行评测基线（-eval）后退出。不调用模型，只压工具链与索引、技能底座，
	// 适合发版前后对比通过率，也方便接 CI 卡回归。
	Eval bool
}

// ParseRunArgs 从命令行参数里识别 -run 模式。
// 支持 "-run 任务"、"--run=任务"、"-run -"（任务从标准输入读）。
func ParseRunArgs(args []string) (RunOptions, bool) {
	opt := RunOptions{Format: "text"}
	found := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-selftest" || a == "--selftest" || a == "/selftest":
			found = true
			opt.SelfTest = true
		case a == "-eval" || a == "--eval" || a == "/eval":
			found = true
			opt.Eval = true
		case a == "--offline" || a == "/offline":
			opt.SelfTestOffline = true
		case a == "--keep" || a == "/keep":
			opt.SelfTestKeep = true
		case a == "-out" || a == "--out" || a == "/out":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				opt.SelfTestOut = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--out="):
			opt.SelfTestOut = strings.TrimPrefix(a, "--out=")
		case a == "-run" || a == "--run" || a == "/run":
			found = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				opt.Prompt = args[i+1]
				i++
			} else if i+1 < len(args) && args[i+1] == "-" {
				opt.Prompt = "-"
				i++
			}
		case strings.HasPrefix(a, "-run=") || strings.HasPrefix(a, "--run="):
			found = true
			opt.Prompt = a[strings.IndexByte(a, '=')+1:]
		case a == "-script" || a == "--script" || a == "/script":
			found = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				opt.Script = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "-script=") || strings.HasPrefix(a, "--script="):
			found = true
			opt.Script = a[strings.IndexByte(a, '=')+1:]
		case a == "--keep-going" || a == "-k":
			opt.KeepGoing = true
		case a == "--shared":
			opt.Shared = true
		case a == "--workdir" || a == "-C" || a == "--dir":
			if i+1 < len(args) {
				opt.WorkDir = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--workdir="):
			opt.WorkDir = strings.TrimPrefix(a, "--workdir=")
		case a == "--yes" || a == "-y" || a == "--force":
			opt.Yes = true
		case a == "--plan":
			opt.Plan = true
		case a == "--quiet" || a == "-q":
			opt.Quiet = true
		case a == "--json":
			opt.Format = "json"
			opt.SelfTestJSON = true
		case a == "--timeout":
			if i+1 < len(args) {
				n := 0
				for _, r := range args[i+1] {
					if r < '0' || r > '9' {
						n = 0
						break
					}
					n = n*10 + int(r-'0')
				}
				opt.Timeout = n
				i++
			}
		}
	}
	return opt, found
}

// Validate 做参数互斥与取值检查，返回给用户看的提示（空字符串表示没问题）。
func (o RunOptions) Validate() string {
	if o.Eval {
		if o.Prompt != "" || o.Script != "" || o.SelfTest {
			return "-eval 与 -run/-script/-selftest 不能同时使用"
		}
		return ""
	}
	if o.SelfTest {
		if o.Prompt != "" || o.Script != "" {
			return "-selftest 与 -run/-script 不能同时使用"
		}
		return ""
	}
	if o.Prompt != "" && o.Script != "" {
		return "-run 与 -script 只能选一个：单任务用 -run，批量用 -script"
	}
	if o.Script == "" && o.Prompt == "" {
		return "缺少任务内容：-run 任务文本 / -script 任务清单.txt / -selftest / -eval"
	}
	return ""
}
