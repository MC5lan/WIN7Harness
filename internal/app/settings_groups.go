//go:build windows

package app

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/ui"
)

// v1.40.2 设置分组：把原来堆在「高级」里的功能按用途拆开，
// 每个分组都是可独立阅读的一张卡，避免用户在长列表里找按钮。

// settingsModelExtras 属于「模型与上下文」：全局指令、生成参数、工具定义规模。
func (a *App) settingsModelExtras(p *settingsPane) {
	c, u := p.c, p.u
	th := a.th
	iw := p.cw - 18 - 44

	rCtx := p.card("注入与上下文", 190)
	ixc := rCtx.X + 22
	iyc := rCtx.Y + 48
	c.TextFace(ixc, iyc, "用户全局指令（单行；长内容用 /instructions）", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	u.TextField("set.sysprompt", ui.R(ixc, iyc+18, iw, 32), "", "例如：回答用中文，先给结论，不要寒暄")
	c.TextFace(ixc, iyc+58, "工具定义 profile", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	profile := strings.TrimSpace(a.ui.Field("set.toolprofile", "").String())
	if profile == "" {
		profile = a.cfg.ToolProfile
	}
	if profile == "" {
		profile = "auto"
	}
	if u.Button("set.toolprofile", "工具配置："+profile+"（点击切换）", ui.R(ixc, iyc+78, 220, 30), BtnGhost) {
		next := "auto"
		switch strings.ToLower(profile) {
		case "auto":
			next = "core"
		case "core":
			next = "full"
		}
		a.ui.SetField("set.toolprofile", next)
		a.Redraw()
	}
	c.TextFace(ixc+232, iyc+80, "auto 在窗口 <64k 或 MCP 工具很多时用 core；full 保留全部工具。",
		ui.FsXs, 400, th.TextMute, ui.FaceUI)
	c.TextFace(ixc, iyc+122, fmt.Sprintf("已禁用技能：%d 个（用 /skills off|on 管理）", len(a.cfg.DisabledSkills)),
		ui.FsXs, 400, th.TextMute, ui.FaceUI)

	rGen := p.card("生成参数", 120)
	ixn := rGen.X + 22
	iyn := rGen.Y + 50
	c.TextFace(ixn, iyn, "温度", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	u.TextField("set.temp", ui.R(ixn, iyn+18, 80, 32), "", "0.3")
	c.TextFace(ixn+100, iyn, "最大输出 token", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	u.TextField("set.maxtok", ui.R(ixn+100, iyn+18, 100, 32), "", "8192")
	c.TextFace(ixn+218, iyn+4, "温度 0 更稳定，写代码建议 0.2~0.4；最大输出按端点上限填。", ui.FsXs, 400, th.TextMute, ui.FaceUI)
}

// settingsAutomation 属于「自动化」：自动验证 + 子任务并发。
func (a *App) settingsAutomation(p *settingsPane) {
	c, u := p.c, p.u
	th := a.th
	r := p.card("自动化执行", 190)
	ix := r.X + 22
	iy := r.Y + 48

	verifyOn := strings.EqualFold(a.cfg.VerifyMode, "auto")
	c.TextFace(ix, iy+4, "自动验证（改完跑测试）", ui.FsSm, 400, th.Text, ui.FaceUI)
	if v := u.ToggleHit("set.verify", ui.R(ix, iy-4, 500, 28), ui.R(ix+170, iy, 38, 20), verifyOn); v != verifyOn {
		if a.streaming || (a.eng != nil && a.eng.Running()) {
			a.toast("生成中不能切换自动验证，先停止或等本轮结束", true)
		} else {
			if v {
				a.cfg.VerifyMode = "auto"
			} else {
				a.cfg.VerifyMode = "off"
			}
			if a.eng != nil {
				a.eng.VerifyMode = a.cfg.VerifyMode
			}
			if err := a.cfg.Save(); err != nil {
				a.toast("保存失败："+err.Error(), true)
			}
		}
	}
	c.TextFace(ix+220, iy+4, "模型准备收尾时自动构建+测试；失败把摘要回灌继续修（最多 3 轮）", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	iy += 44

	c.TextFace(ix, iy+4, "子任务并行上限", ui.FsSm, 400, th.Text, ui.FaceUI)
	if u.Button("set.st-", "−", ui.R(ix+120, iy, 26, 24), BtnGhost) {
		if a.cfg.MaxSubTasks > 1 {
			a.cfg.MaxSubTasks--
		}
	}
	c.TextFace(ix+160, iy+5, strconv.Itoa(a.cfg.MaxSubTasks), ui.FsSm, 700, th.Text, ui.FaceMono)
	if u.Button("set.st+", "+", ui.R(ix+186, iy, 26, 24), BtnGhost) {
		if a.cfg.MaxSubTasks < 8 {
			a.cfg.MaxSubTasks++
		}
	}
	c.TextFace(ix+222, iy+4, "1~8，task 批量派发的并发数", ui.FsXs, 400, th.TextMute, ui.FaceUI)
}

// settingsHooks 属于「自动化」：工具钩子。
func (a *App) settingsHooks(p *settingsPane) {
	c, u := p.c, p.u
	th := a.th
	iw := p.cw - 18 - 44
	hks := a.cfg.Hooks
	hooksH := int32(150) + int32(len(hks))*34
	if len(hks) == 0 {
		hooksH = 190
	}
	r := p.card("工具钩子（hooks）", hooksH)
	ix := r.X + 22
	iy := r.Y + 48
	if len(hks) == 0 {
		c.TextFace(ix, iy, "还没有钩子。钩子能在工具执行前后自动跑本地命令，例如改完文件自动 gofmt。", ui.FsXs, 400, th.TextMute, ui.FaceUI)
		iy += 26
	}
	for i := range hks {
		h := hks[i]
		row := ui.R(ix, iy, iw, 30)
		c.Round(row, 6, ui.Blend(th.Surface, th.Bg, 0.55))
		c.TextFace(row.X+10, row.Y+7, h.Event+" · "+h.Match, ui.FsXs, 700, th.Text, ui.FaceMono)
		cm := h.Command
		if len([]rune(cm)) > 46 {
			cm = string([]rune(cm)[:45]) + "…"
		}
		c.TextFace(row.X+210, row.Y+7, cm, ui.FsXs, 400, th.TextDim, ui.FaceMono)
		if u.Button("hook.del:"+strconv.Itoa(i), "删除", ui.R(row.Right()-58, row.Y+3, 52, 24), BtnSubtle) {
			a.cfg.Hooks = append(a.cfg.Hooks[:i], a.cfg.Hooks[i+1:]...)
			if err := a.cfg.Save(); err != nil {
				a.toast("保存失败："+err.Error(), true)
			} else {
				a.toast("已删除钩子", false)
			}
			a.Redraw()
		}
		iy += 34
	}
	c.TextFace(ix, iy, "新增：事件 / 工具匹配 / 命令", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	iy += 18
	u.TextField("hook.event", ui.R(ix, iy, 118, 32), "", "post_tool")
	u.TextField("hook.match", ui.R(ix+126, iy, 152, 32), "", "write_file,edit_file")
	u.TextField("hook.cmd", ui.R(ix+286, iy, iw-286-84, 32), "", "gofmt -w .")
	if u.Button("hook.add", "添加", ui.R(ix+iw-76, iy, 76, 32), BtnGhost) {
		a.addHook()
	}
	iy += 40
	c.TextFace(ix, iy, "事件限 pre_tool / post_tool / turn_end；pre_tool 以非零退出码结束会阻止本次调用，超时默认 30 秒。", ui.FsXs, 400, th.TextMute, ui.FaceUI)
}

// settingsContextPresets 属于「模型与上下文」。
func (a *App) settingsContextPresets(p *settingsPane) {
	c, u := p.c, p.u
	th := a.th
	iw := p.cw - 18 - 44
	r := p.card("上下文窗口", 150)
	ix := r.X + 22
	iy := r.Y + 48
	c.TextFace(ix, iy, "窗口预设", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	px := ix + 100
	curWin := a.cfg.ContextWindow
	if s := strings.TrimSpace(a.ui.Field("set.ctxwin", "").String()); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			curWin = n
		}
	}
	for _, opt := range []struct {
		label string
		val   int
	}{
		{"auto", 0}, {"8k", 8000}, {"32k", 32000}, {"128k", 128000},
		{"200k", 200000}, {"300k", 300000}, {"600k", 600000}, {"960k", 960000},
	} {
		kind := BtnGhost
		if curWin == opt.val {
			kind = BtnPrimary
		}
		if u.Button("set.ctx."+opt.label, opt.label, ui.R(px, iy-6, 54, 28), kind) {
			a.ui.SetField("set.ctxwin", strconv.Itoa(opt.val))
			if opt.val >= 300000 && runtime.GOARCH == "386" {
				a.toast("32 位进程会保留约 60k token 的压缩预算上限，超大窗口不会按满窗口分配内存", true)
			}
			a.Redraw()
		}
		px += 58
	}
	c.TextFace(ix, iy+28, "大窗口选 300k/600k/960k；保存后生效", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	if cap := core.SuggestModelCapabilities(a.cfg.Provider().BaseURL, a.convModel()); cap.ContextWindow > 0 {
		note := fmt.Sprintf("模型推测窗口：%dk（%s，仅供参考）", cap.ContextWindow/1000, cap.Source)
		c.TextFace(ix, iy+50, ellipsis(c, note, ui.FsXs, 400, iw-90), ui.FsXs, 400, th.TextMute, ui.FaceUI)
		if curWin != cap.ContextWindow {
			if u.Button("set.ctx.suggest", "采用建议", ui.R(ix+iw-76, iy+46, 76, 24), BtnGhost) {
				a.ui.SetField("set.ctxwin", strconv.Itoa(cap.ContextWindow))
				a.Redraw()
			}
		}
	}
}

// settingsGitAbout 属于「工具与关于」。
func (a *App) settingsGitAbout(p *settingsPane) {
	c, u := p.c, p.u
	th := a.th
	iw := p.cw - 18 - 44
	gitCardH := int32(160)
	if iw < 600 {
		gitCardH = 222
	}
	rGit := p.card("Git 身份（提交作者）", gitCardH)
	ixg := rGit.X + 22
	yyg := rGit.Y + 50
	git := a.uiData()
	gName, gEmail, gTemp := git.gitName, git.gitEmail, git.gitTemp
	if git.gitAt.IsZero() {
		c.TextFace(ixg, yyg, "正在读取 Git 身份…", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	} else {
		if a.ui.Field("set.gitname", "").String() == "" && gName != "" {
			a.ui.SetField("set.gitname", gName)
			a.ui.SetField("set.gitmail", gEmail)
		}
		note := "当前：" + gName + " <" + gEmail + ">"
		if gTemp {
			note += "（临时身份，提交历史里会显示成这个名字）"
		}
		c.TextFace(ixg, yyg, ellipsis(c, note, ui.FsXs, 400, iw), ui.FsXs, 400, th.TextMute, ui.FaceUI)
	}
	yyg += 22
	if iw >= 600 {
		c.TextFace(ixg, yyg, "名字", ui.FsXs, 500, th.TextDim, ui.FaceUI)
		u.TextField("set.gitname", ui.R(ixg, yyg+18, 180, 32), "", "你的名字")
		c.TextFace(ixg+196, yyg, "邮箱", ui.FsXs, 500, th.TextDim, ui.FaceUI)
		u.TextField("set.gitmail", ui.R(ixg+196, yyg+18, 220, 32), "", "you@example.com")
		if u.Button("set.gitsave", "写入 Git 全局配置", ui.R(ixg+432, yyg+18, 160, 32), BtnGhost) {
			name := strings.TrimSpace(u.Field("set.gitname", "").String())
			mail := strings.TrimSpace(u.Field("set.gitmail", "").String())
			if err := core.SetGitIdentity(name, mail); err != nil {
				a.toast(err.Error(), true)
			} else {
				a.toast("已写入 Git 全局身份："+name+" <"+mail+">", false)
			}
			a.Redraw()
		}
	} else {
		c.TextFace(ixg, yyg, "名字", ui.FsXs, 500, th.TextDim, ui.FaceUI)
		u.TextField("set.gitname", ui.R(ixg, yyg+18, iw, 32), "", "你的名字")
		yyg += 58
		c.TextFace(ixg, yyg, "邮箱", ui.FsXs, 500, th.TextDim, ui.FaceUI)
		u.TextField("set.gitmail", ui.R(ixg, yyg+18, iw, 32), "", "you@example.com")
		if u.Button("set.gitsave", "写入 Git 全局配置", ui.R(ixg, yyg+58, 160, 32), BtnGhost) {
			name := strings.TrimSpace(u.Field("set.gitname", "").String())
			mail := strings.TrimSpace(u.Field("set.gitmail", "").String())
			if err := core.SetGitIdentity(name, mail); err != nil {
				a.toast(err.Error(), true)
			} else {
				a.toast("已写入 Git 全局身份："+name+" <"+mail+">", false)
			}
			a.Redraw()
		}
	}

	rAbout := p.card("关于", 120)
	ixa := rAbout.X + 22
	iya := rAbout.Y + 48
	c.TextFace(ixa, iya, "WIN7 Harness · Win7 SP1 原生自绘 UI · Go 1.20.14 · 单文件 EXE", ui.FsXs, 500, th.TextDim, ui.FaceUI)
	c.TextFace(ixa, iya+22, "只兼容 OpenAI Chat Completions 协议；模型名从端点动态获取，不随应用内置。", ui.FsXs, 400, th.TextMute, ui.FaceUI)
	c.TextFace(ixa, iya+44, "OpenCode Go / Go Plus 使用 "+core.OpenCodeGoBaseURL+"，模型 ID 粘贴 opencode-go/xxx 也会自动去掉前缀。", ui.FsXs, 400, th.TextMute, ui.FaceUI)
}
