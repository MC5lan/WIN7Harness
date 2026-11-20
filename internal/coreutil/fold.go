package coreutil

import "time"

// 工具卡折叠的纯逻辑：把"连续多少条已结束的工具卡可以收成一行"从 UI 里剥出来。
// 放在 core 里的原因很实际——app 包带 win32 构建标签，在当前开发机上跑不了单测，
// 折叠是唯一会影响"用户看到几条卡"的逻辑，不能只靠肉眼看界面来验证。

// ToolSpan 是折叠判定需要的最小信息。
type ToolSpan struct {
	Done    bool
	Fail    bool
	Elapsed time.Duration
}

// FoldRun 描述一段被折叠的工具卡：从 Start 开始的 Len 条，其中 Fails 条失败，累计耗时 Dur。
type FoldRun struct {
	Start int
	Len   int
	Fails int
	Dur   time.Duration
}

// FoldMinRun 连续多少条已结束的工具卡才值得折叠。
// 两条不折——用户一眼能看完；三条起才占屏。
const FoldMinRun = 3

// FoldRuns 扫描一条消息里的工具卡，返回应当折叠的组。
// open 是用户手动展开过的组起始下标集合，展开过的组不再折叠（保持原位逐条显示）。
//
// 关键约束：只折"已结束"的卡。正在运行的卡必须逐条可见，
// 否则用户看到一行"执行了 5 步"却不知道现在卡在哪一步。
func FoldRuns(spans []ToolSpan, open map[int]bool) []FoldRun {
	if len(spans) < FoldMinRun {
		return nil
	}
	var out []FoldRun
	for i := 0; i < len(spans); {
		if !spans[i].Done {
			i++
			continue
		}
		j := i
		var dur time.Duration
		fails := 0
		for j < len(spans) && spans[j].Done {
			dur += spans[j].Elapsed
			if spans[j].Fail {
				fails++
			}
			j++
		}
		if n := j - i; n >= FoldMinRun && !open[i] {
			out = append(out, FoldRun{Start: i, Len: n, Fails: fails, Dur: dur})
		}
		i = j
	}
	return out
}
