package core

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// checkpoint 把"快照"变成用户能看懂的东西。
//
// 原来只有 /undo：按一次退一步，看不见还剩几步、也不知道退的是什么文件。
// 多轮改动之后再想看"我都动了哪些文件"就只能靠记忆。这里补两件事：
// 列出改动清单、一次退多步。

// Checkpoint 一条可回退的改动记录。
type Checkpoint struct {
	Index   int    // 1 = 最近一次改动；数字越大越早
	Path    string // 被改的文件
	At      string // 改动时间（本地格式，便于阅读）
	Existed bool   // 改动前文件已存在：回退恢复内容；false：本次新建，回退即删除
}

// ListCheckpoints 列出最近 limit 条改动（全部会话），最新在前。limit<=0 时取全部（上限 200）。
func ListCheckpoints(limit int) []Checkpoint { return ListCheckpointsConv(limit, "") }

// ListCheckpointsConv 只列当前会话的改动（convID 为空 = 不过滤），与 UndoLast 行为一致。
// 用 Checkpoint{序号, 路径, 时间, 是否存在}，序号 1 = 最近一条。
func ListCheckpointsConv(limit int, convID string) []Checkpoint {
	entries := scanSnapshots(SnapshotsDir())
	var items []snapshotEntry
	for _, e := range entries {
		if !e.metaOK {
			continue
		}
		m := e.meta
		// conv 过滤：空 = 不过滤；否则只收当前会话（旧档无 ConvID 的也收进来，兼容老快照）
		if convID != "" && m.ConvID != "" && m.ConvID != convID {
			continue
		}
		items = append(items, e)
	}
	// id 是 UnixNano 补零，字典序即时间序；最新在前
	sort.Slice(items, func(i, j int) bool { return items[i].id > items[j].id })

	if limit <= 0 || limit > len(items) {
		limit = len(items)
	}
	out := make([]Checkpoint, 0, limit)
	for i := 0; i < limit; i++ {
		m := items[i].meta
		at := m.At
		if t, err := time.Parse(time.RFC3339, m.At); err == nil {
			at = t.Local().Format("01-02 15:04:05")
		}
		out = append(out, Checkpoint{Index: i + 1, Path: m.Path, At: at, Existed: m.Existed})
	}
	return out
}

// RewindSteps 回退最近 n 步改动，返回逐步结果。遇到失败即停，已成功的部分不回滚。
func RewindSteps(n int) ([]UndoResult, error) {
	if n <= 0 {
		n = 1
	}
	var done []UndoResult
	for i := 0; i < n; i++ {
		res, err := UndoLast()
		if err != nil {
			if len(done) == 0 {
				return nil, err
			}
			// 中间断了：把已经退掉的步数如实报回去，让调用方知道当前停在哪
			return done, fmt.Errorf("已回退 %d 步后停止：%v", len(done), err)
		}
		done = append(done, res)
	}
	return done, nil
}

// FormatCheckpoints 渲染成命令行可直接打印的多行文本。
func FormatCheckpoints(cps []Checkpoint) string {
	if len(cps) == 0 {
		return "暂无改动记录（只有本次会话里写过文件才会有快照）"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("最近 %d 处改动（1 = 最新，可用 /rewind N 回退最近 N 步）：\n", len(cps)))
	for _, c := range cps {
		verb := "覆盖前备份"
		if !c.Existed {
			verb = "新建（回退将删除）"
		}
		sb.WriteString(fmt.Sprintf("  %2d. %s  %s  [%s]\n", c.Index, c.At, c.Path, verb))
	}
	return strings.TrimRight(sb.String(), "\n")
}
