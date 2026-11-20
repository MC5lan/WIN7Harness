package app

import (
	"fmt"

	"win7harness/internal/ui"
)

// paintTaskSide 在右侧检查器顶部画"任务栏"：目标收敛进度、待办清单、在跑的子任务。
// 返回绘制后的纵坐标，供调用方继续往下排版。
func (a *App) paintTaskSide(u *UI, x, w, H, yy int32) int32 {
	if a.eng == nil {
		return yy
	}
	snap := a.eng.Board().Snapshot()
	if snap.Empty() {
		return yy
	}
	c := u.c
	th := a.th
	c.TextFace(x+16, yy, "任务栏", ui.FsSm, 700, th.TextDim, ui.FaceUI)
	if snap.Running > 0 {
		tag := fmt.Sprintf("子任务 ×%d", snap.Running)
		tw := c.MeasureFace(tag, ui.FsXs, 400, ui.FaceUI)
		c.TextFace(x+w-16-tw, yy, tag, ui.FsXs, 400, th.Warn, ui.FaceUI)
	}
	yy += 22

	line := func(mark string, col uint32, text string) {
		c.TextFace(x+16, yy, mark, ui.FsXs, 700, col, ui.FaceUI)
		c.TextFace(x+32, yy, ellipsis(c, text, ui.FsXs, 400, w-56), ui.FsXs, 400, th.TextDim, ui.FaceUI)
		yy += 19
	}

	// 目标收敛：进行中优先显示实时步骤，结束后显示上次结果。
	if snap.GoalRun != "" {
		line("◐", th.Accent, "目标："+snap.GoalRun)
		if snap.Phase != "" {
			line("·", th.TextMute, snap.Phase)
		}
	} else if snap.Goal != nil {
		g := snap.Goal
		col := th.TextMute
		mark := "○"
		switch g.Stop {
		case "achieved":
			col, mark = th.Success, "✓"
		case "max-rounds", "stalled":
			col, mark = th.Warn, "!"
		case "cancelled":
			col = th.TextMute
		}
		line(mark, col, "目标 "+g.Progress()+"："+g.Goal)
		for i, s := range g.Steps {
			if yy > H-40 {
				return yy
			}
			m, mc := "○", th.TextMute
			if s.Done {
				m, mc = "✓", th.Success
			}
			line(m, mc, fmt.Sprintf("%d. %s", i+1, s.Desc))
		}
	}

	// 待办清单：doing 高亮，done 打勾。
	if len(snap.Todos) > 0 {
		for _, t := range snap.Todos {
			if yy > H-40 {
				return yy
			}
			switch t.Status {
			case "done":
				line("✓", th.Success, t.Text)
			case "doing":
				line("◐", th.Accent, t.Text)
			default:
				line("○", th.TextMute, t.Text)
			}
		}
		if snap.AllCount > 0 {
			pct := fmt.Sprintf("%d/%d 完成", snap.DoneCount, snap.AllCount)
			c.TextFace(x+16, yy, pct, ui.FsXs, 400, th.TextMute, ui.FaceUI)
			yy += 18
		}
	}

	yy += 4
	c.HLine(x+16, yy, w-32, th.BorderSoft)
	return yy + 12
}
