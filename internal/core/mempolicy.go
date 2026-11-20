package core

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sync/atomic"
)

// 低内存模式：面向 32 位进程 + 小内存机器（Win7 老本常见 2GB 内存、单进程上限 2GB 地址空间）。
// 打开后按档位收紧几处最吃内存的地方：@ 引用预算、工具输出缓冲、代码索引规模、
// 图片编码尺寸与单条消息图片数、批量子任务并发。所有阈值都在调用点实时读取，
// 因此切换开关后下一次调用立即生效，不需要重启（索引规模需 /index 重建才回落）。
var lowMemory int32

// SetLowMemory 打开或关闭低内存模式。
func SetLowMemory(on bool) {
	if on {
		atomic.StoreInt32(&lowMemory, 1)
	} else {
		atomic.StoreInt32(&lowMemory, 0)
	}
}

// LowMemory 返回当前是否处于低内存模式。
func LowMemory() bool {
	return atomic.LoadInt32(&lowMemory) == 1
}

// MemoryLimitBytes 返回建议的 Go 堆软上限。
//
// 这不是“物理内存”，而是 Go runtime 触发更积极 GC 的阈值：
//   - 386 进程地址空间只有 ~2GB，必须给 DLL/栈/GDI/非 Go 内存留余量；
//   - amd64 也设一个上限，防止长会话/大文件把整机吃满。
//
// 返回值 <=0 表示不设置。GOMEMLIMIT 环境变量优先，用户显式配置时不覆盖。
func MemoryLimitBytes() int64 {
	return memoryLimitFor(DetectProfile().TotalMB, runtime.GOARCH)
}

func memoryLimitFor(totalMB int, arch string) int64 {
	const mb = int64(1 << 20)
	if arch == "386" {
		limit := int64(1200) * mb
		if totalMB > 0 && totalMB < 1536 {
			limit = int64(totalMB) * mb * 55 / 100
			if limit < 512*mb {
				limit = 512 * mb
			}
		}
		return limit
	}
	if totalMB <= 0 {
		// 内存探测失败：按 1.5GB 兜底，至少不会让一个失控会话把整机吃光。
		return 1536 * mb
	}
	limit := int64(totalMB) * mb * 65 / 100
	if limit < 1024*mb {
		limit = 1024 * mb
	}
	if limit > 6144*mb {
		limit = 6144 * mb
	}
	return limit
}

// ApplyMemoryPolicy 在进程启动早期调用：设置 Go 软内存上限，
// 小内存/386 机器再收紧 GOGC，降低峰值（代价是更多 GC CPU）。
// 返回设置的字节数；0 表示未设置（GOMEMLIMIT 已由用户指定）。
func ApplyMemoryPolicy() int64 {
	if v := os.Getenv("GOMEMLIMIT"); v != "" {
		return 0
	}
	lim := MemoryLimitBytes()
	if lim > 0 {
		debug.SetMemoryLimit(lim)
	}
	if runtime.GOARCH == "386" || DetectProfile().Tier == "2G" {
		debug.SetGCPercent(70)
	}
	return lim
}

// MemLimits 是一组与内存占用相关的阈值。
type MemLimits struct {
	// AtRefBudget @ 引用注入模型的内容预算（字节）
	AtRefBudget int
	// ToolOutput 单次工具输出上限（字节）
	ToolOutput int
	// IndexFiles 代码索引最多收录的文件数
	IndexFiles int
	// IndexSymbols 代码索引最多收录的符号数
	IndexSymbols int
	// ImageSide 送模型图片的最长边
	ImageSide int
	// ImageBytes 单张图片编码后体积上限（字节）
	ImageBytes int
	// ImagesPerMessage 单条消息最多图片数
	ImagesPerMessage int
	// SubTasks 批量派发子任务的默认并发上限
	SubTasks int
	// SubAgentTurns 子 Agent 单次任务的最大轮数
	SubAgentTurns int
}

// MemProfile 返回当前档位下的阈值。
// v1.29：从"手动开关两档"升级为"按物理内存自动三档"——
// 老机用户不会主动去点低内存模式，等他因为 OOM 来找你时已经没法用了。
// 显式打开低内存开关仍然最高优先，用户说了算。
func MemProfile() MemLimits {
	if LowMemory() {
		return lowTierLimits()
	}
	switch DetectProfile().Tier {
	case "2G":
		return lowTierLimits()
	case "4G", "未知":
		// 未知（内存探测失败）按中档给——宁可保守，也不能按最宽分配
		return midTierLimits()
	}
	return stdLimits()
}

func lowTierLimits() MemLimits {
	return MemLimits{
		AtRefBudget:      48 * 1024,
		ToolOutput:       96 * 1024,
		IndexFiles:       1200,
		IndexSymbols:     6000,
		ImageSide:        1024,
		ImageBytes:       640 * 1024,
		ImagesPerMessage: 2,
		SubTasks:         1,
		SubAgentTurns:    12,
	}
}

func midTierLimits() MemLimits {
	return MemLimits{
		AtRefBudget:      80 * 1024,
		ToolOutput:       160 * 1024,
		IndexFiles:       2000,
		IndexSymbols:     12000,
		ImageSide:        1280,
		ImageBytes:       900 * 1024,
		ImagesPerMessage: 3,
		SubTasks:         2,
		SubAgentTurns:    24,
	}
}

func stdLimits() MemLimits {
	return MemLimits{
		AtRefBudget:      120 * 1024,
		ToolOutput:       256 * 1024,
		IndexFiles:       3000,
		IndexSymbols:     20000,
		ImageSide:        MaxImageSide,
		ImageBytes:       MaxImageBytes,
		ImagesPerMessage: MaxImagesPerMessage,
		SubTasks:         3,
		SubAgentTurns:    40,
	}
}

// ModeName 返回档位名称。
func (m MemLimits) ModeName() string {
	if LowMemory() {
		return "低内存（手动开启）"
	}
	switch p := DetectProfile(); p.Tier {
	case "2G":
		return "低内存（按 " + p.Tier + " 内存自动收紧）"
	case "4G", "未知":
		// 未知档表示内存没测出来（权限/WMI 异常），按中档用
		return "中等（内存未探测，按中档用）"
	}
	return "标准"
}

// Describe 生成可在 /memory 与设置页展示的多行说明。
func (m MemLimits) Describe() string {
	return fmt.Sprintf(
		"档位：%s\n"+
			"- @ 引用注入上限：%s\n"+
			"- 工具单次输出上限：%s\n"+
			"- 代码索引上限：%d 个文件 / %d 个符号\n"+
			"- 图片：最长边 %d px，单张 ≤ %s，每条消息 ≤ %d 张\n"+
			"- 批量子任务并发：%d（子 Agent 单任务最多 %d 轮）",
		m.ModeName(),
		HumanSize(int64(m.AtRefBudget)),
		HumanSize(int64(m.ToolOutput)),
		m.IndexFiles, m.IndexSymbols,
		m.ImageSide, HumanSize(int64(m.ImageBytes)), m.ImagesPerMessage,
		m.SubTasks, m.SubAgentTurns,
	)
}
