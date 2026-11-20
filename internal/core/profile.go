package core

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
)

// v1.29 把"能拿单测覆盖的纯逻辑"从 app 包里抽出来：
// app 包带 windows build tag，在开发机上跑不了测试，
// 结果就是命令解析这类分支密集的代码一直靠手点验证。
// 放在 core 里，既能被 app 调用，又能直接 go test。

// UpdateCmd 解析 /update 后面的子命令。
// 返回 mode："" 表示只看状态；"apply" 表示真替换；
// 只有 apply 才关心 force（是否跳过确认）。
func UpdateCmd(rest string) (mode string, force bool) {
	f := strings.Fields(strings.ToLower(strings.TrimSpace(rest)))
	if len(f) == 0 {
		return "", false
	}
	switch f[0] {
	case "apply", "装", "替换", "install", "upgrade":
		mode = "apply"
	case "status", "状态", "check", "检查":
		mode = ""
	default:
		return "", false
	}
	for _, w := range f[1:] {
		if w == "force" || w == "强制" || w == "-f" {
			force = true
		}
	}
	return mode, force
}

// QueueCmd 解析 /queue 子命令：list / clear / run。
func QueueCmd(rest string) string {
	f := strings.Fields(strings.ToLower(strings.TrimSpace(rest)))
	if len(f) == 0 {
		return "list"
	}
	switch f[0] {
	case "clear", "清空", "flush":
		return "clear"
	case "run", "start", "开始", "马上":
		return "run"
	}
	return "list"
}

// DeviceCmd 解析 /device 子命令，返回动作与目标（目标仅 revoke 用）。
func DeviceCmd(rest string) (action, target string) {
	f := strings.Fields(strings.TrimSpace(rest))
	if len(f) == 0 {
		return "list", ""
	}
	switch strings.ToLower(f[0]) {
	case "revoke", "del", "踢", "下线":
		return "revoke", strings.Join(f[1:], " ")
	case "prune", "clean", "清理":
		return "prune", ""
	case "list", "列表":
		return "list", ""
	}
	// 纯目标（/device 小马手机）当成吊销，少打一个字。
	return "revoke", strings.Join(f, " ")
}

// StopReport 手机端点"停止"时的统一话术。
// 单独抽出来是为了两个保证：没在跑时必须说"没有任务"而不是假装停了；
// 停了什么要逐项列清楚，用户才知道队列是不是也被清了。
func StopReport(engineRunning bool, queued int) (string, bool) {
	parts := []string{}
	if engineRunning {
		parts = append(parts, "当前生成")
	}
	if queued > 0 {
		parts = append(parts, fmt.Sprintf("队列中 %d 个任务", queued))
	}
	if len(parts) == 0 {
		return "当前没有正在跑的任务", false
	}
	return "已停止 " + strings.Join(parts, " 与 "), true
}

// PlanToggleText 计划启停后的回执文案。
func PlanToggleText(name string, on bool) string {
	state := "暂停"
	if on {
		state = "启用"
	}
	return fmt.Sprintf("已%s：%s", state, clipLocal(name, 24))
}

func clipLocal(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// —— D 批：Win7 内存画像 ——
//
// Win7 常见 2G/4G 老机，且 32 位进程可用地址空间只有 ~2G（含碎片）。
// 默认值按"开发机 16G"调出来的缓存，在 2G 机器上就是定时 OOM。
// 这里按 runtime 读到的物理内存给三档，所有上限都从这里取。

// Profile 一台机器该用多大的预算。
type Profile struct {
	TotalMB    int // 物理内存（MB）
	Tier       string
	HistoryMax int // 单个会话保留的消息条数上限
	CacheMB    int // 索引/文件缓存预算（MB）
	Workers    int // 并行解析文件数
	BigFileMB  int // 超过这个大小的文件不整读，走流式/跳过
}

var (
	profileOnce sync.Once
	profileVal  Profile
)

// DetectProfile 读物理内存给出预算，结果缓存：
// 内存不会中途变，而 MemProfile 会在每个工具调用点被读，不能每次都 syscall。
func DetectProfile() Profile {
	profileOnce.Do(func() {
		total := 0
		if kb, ok := totalMemoryKB(); ok {
			total = int(kb / 1024)
		}
		profileVal = profileFor(total)
	})
	return profileVal
}

// profileFor 把内存大小映射成预算（纯函数，便于单测）。
func profileFor(totalMB int) Profile {
	p := Profile{TotalMB: totalMB}
	switch {
	// 探测失败（TotalMB<=0）必须单独处理：以前它会落进 <=4096 分支被当成 4G，
	// 界面上堂而皇之写着"4G"，实际这台机器到底多少内存没人知道。
	// 现在标"未知"，预算按中档给——宁可保守，也不能凭猜报档。
	case totalMB <= 0:
		p.Tier = "未知"
		p.HistoryMax = 120
		p.CacheMB = 64
		p.Workers = 3
		p.BigFileMB = 16
	case totalMB <= 2048:
		p.Tier = "2G"
		p.HistoryMax = 60
		p.CacheMB = 24
		p.Workers = 2
		p.BigFileMB = 8
	case totalMB <= 4096:
		p.Tier = "4G"
		p.HistoryMax = 120
		p.CacheMB = 64
		p.Workers = 3
		p.BigFileMB = 16
	default:
		// 4G 以上：按大内存给足预算。
		p.Tier = "8G+"
		p.HistoryMax = 200
		p.CacheMB = 128
		p.Workers = maxWorkers()
		p.BigFileMB = 32
	}
	return p
}

func maxWorkers() int {
	n := runtime.NumCPU()
	if n < 4 {
		return 4
	}
	if n > 8 {
		return 8
	}
	return n
}

// Text 给人看的一段描述。
func (p Profile) Text() string {
	return fmt.Sprintf("内存画像：%s（约 %d MB）· 会话保留 %d 条 · 缓存预算 %d MB · 并行 %d · 大文件阈值 %d MB",
		p.Tier, p.TotalMB, p.HistoryMax, p.CacheMB, p.Workers, p.BigFileMB)
}
