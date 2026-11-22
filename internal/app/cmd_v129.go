package app

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/coreutil"
)

// v1.29 新增命令：/perf /queue /device，以及 /update apply。
// 风格与 v1.27/v1.28 保持一致：优先给"现在是什么状态 + 下一步能敲什么"。

// —— /perf 运行指标 ——

func (a *App) cmdPerf(rest string) {
	st := a.v29
	if st == nil {
		a.toast("指标未初始化", true)
		return
	}
	arg := strings.ToLower(strings.TrimSpace(rest))
	// 无参跳用量页的"工具耗时"栏：P95 排序在表格里比一段文本好找。
	if arg == "" {
		a.page = PageUsage
		a.useTab = useTabPerf
		return
	}
	switch arg {
	case "clear", "reset", "清空":
		st.metrics = coreutil.NewMetrics(2048)
		a.sysNote("运行指标已清空。")
		return
	}
	snap := st.metrics.Snapshot()
	b := &strings.Builder{}
	b.WriteString(coreutil.MetricsText(snap))
	b.WriteString("\r\n")
	if a.v27 != nil && a.v27.rctl != nil {
		b.WriteString("手机端：/api/metrics 同一份数据；诊断包里也会带上。\r\n")
	}
	b.WriteString("清空：/perf clear")
	a.sysNote(b.String())
}

// —— /queue 任务队列 ——

func (a *App) cmdQueue(rest string) {
	st := a.v29
	if st == nil {
		a.toast("队列未初始化", true)
		return
	}
	arg := strings.ToLower(strings.TrimSpace(rest))
	// 不带参数就打开自动化页：队列里有什么、能不能撤，界面比一段文字有用得多。
	if arg == "" {
		a.page = PageAuto
		a.autoTab = autoTabQueue
		return
	}
	switch arg {
	case "clear", "清空":
		n := st.queue.Clear()
		a.sysNote(fmt.Sprintf("已清空队列，丢弃 %d 个待执行任务。", n))
		return
	case "run", "start", "开始":
		if a.streaming {
			a.toast("正在生成，跑完自动继续", true)
			return
		}
		a.drainQueue(time.Now())
		a.sysNote("已尝试立即执行队首任务。")
		return
	}
	items := st.queue.List()
	b := &strings.Builder{}
	if len(items) == 0 {
		b.WriteString("队列是空的。\r\n")
	} else {
		fmt.Fprintf(b, "待执行 %d 个（上限 %d）\r\n", len(items), 64)
		for i, it := range items {
			fmt.Fprintf(b, "%d. [%s] %s\r\n", i+1, it.Source, oneLine(clipRunes(it.Prompt, 60)))
		}
	}
	b.WriteString("\r\n忙碌时定时任务与手机指令会排在这里，不再被丢弃。\r\n清空：/queue clear · 立即执行队首：/queue run")
	a.sysNote(b.String())
}

// —— /device 设备管理 ——

func (a *App) cmdDevice(rest string) {
	st := a.v29
	if st == nil {
		a.toast("设备表未初始化", true)
		return
	}
	f := strings.Fields(rest)
	if len(f) == 0 {
		// 设备列表做成页面：能直接吊销丢掉的手机，这是安全操作，不该只给一段文字。
		a.page = PageExt
		a.extTab = extTabDevice
		return
	}
	if strings.EqualFold(f[0], "list") {
		list := st.devices.List()
		b := &strings.Builder{}
		if len(list) == 0 {
			b.WriteString("还没有已配对的手机。\r\n")
		} else {
			fmt.Fprintf(b, "已配对 %d 台：\r\n", len(list))
			for i, d := range list {
				seen := "从未"
				if !d.LastSeen.IsZero() {
					seen = d.LastSeen.Format("01-02 15:04")
				}
				fmt.Fprintf(b, "%d. %s（%s）最近使用 %s · 到期 %s\r\n",
					i+1, d.Name, d.ID, seen, d.ExpiresAt.Format("01-02 15:04"))
			}
		}
		b.WriteString("\r\n令牌已落盘（只存哈希），重启电脑手机不用重新扫码。\r\n" +
			"吊销：/device revoke 名称或ID · 清理过期：/device prune")
		a.sysNote(b.String())
		return
	}
	switch strings.ToLower(f[0]) {
	case "revoke", "del", "踢", "下线":
		if len(f) < 2 {
			a.toast("用法：/device revoke 名称或ID", true)
			return
		}
		key := strings.Join(f[1:], " ")
		if d, ok := st.devices.Revoke(key); ok {
			a.sysNote(d.Name + " 已下线，需要用配对码重新扫码。")
			// 内存里的 token 也要一起清，否则本次进程内还能用。
			if a.v27 != nil && a.v27.rctl != nil {
				if d.Tok != "" {
					a.v27.rctl.ForgetToken(d.Tok)
				}
			}
			return
		}
		a.toast("没找到这台设备："+key, true)
	case "prune", "clean", "清理":
		n := st.devices.Prune(time.Now())
		a.sysNote(fmt.Sprintf("已清理 %d 台过期设备。", n))
	default:
		a.toast("用法：/device [list|revoke 名称|prune]", true)
	}
}

// —— /update apply：真正替换 exe 并重启 ——

func (a *App) cmdUpdateApply(rest string) {
	arg := strings.ToLower(strings.TrimSpace(rest))
	force := strings.Contains(arg, "force") || strings.Contains(arg, "强制")
	url := strings.TrimSpace(a.cfg.UpdateURL)
	if url == "" {
		url = core.DefaultManifestURL
	}
	exe, err := core.SelfExePath()
	if err != nil {
		a.toast("拿不到自身路径："+err.Error(), true)
		return
	}
	dir := core.UpdatesDir()
	if err := core.PrepareUpdate(dir); err != nil {
		a.sysNote("自更新前检查失败：" + err.Error())
		return
	}
	uc, newer, err := core.CheckUpdateAny(core.Version, a.cfg.UpdateSource, a.cfg.UpdateRepo, url, core.HTTPFetcher)
	if err != nil {
		a.sysNote("检查更新失败：" + err.Error())
		return
	}
	if !newer {
		a.sysNote("已是最新版本（" + core.Version + "），无需替换。")
		return
	}
	// 所有用户安装（Program Files）普通用户不可写；应用内替换必然失败。
	// 下载前给出可执行的出路，不要等下载完才报错。
	if !core.InstallDirWritable() {
		a.sysNote("当前程序目录不可写（通常是“所有用户”安装或只读介质）。\r\n" +
			"请下载新版安装包，右键以管理员身份运行升级；应用内替换已停用。\r\n" +
			"配置和会话在用户目录，升级不会丢。")
		return
	}
	f := uc.File
	if f.URL == "" || f.SHA256 == "" {
		a.sysNote(fmt.Sprintf("更新源里没有 %s 架构的包或 SHA256，已放弃。", runtime.GOARCH))
		return
	}
	a.statusMsg = "正在下载更新包…"
	newExe, err := core.DownloadUpdate(f, runtime.GOARCH, core.HTTPFetcher)
	a.statusMsg = ""
	if err != nil {
		a.sysNote("下载或校验失败：" + err.Error())
		return
	}
	// 再按当前 exe 头校验一次架构：清单写对不等于文件对。
	if want := core.ExeArchOfSelf(); want != "" {
		if err := core.CheckUpdateArch(newExe, want); err != nil {
			a.sysNote("更新包架构不匹配，已放弃替换：" + err.Error())
			return
		}
	}
	if !force {
		_ = core.StageUpdate(dir, newExe)
		a.sysNote(fmt.Sprintf("新版本 %s 已下载并通过 SHA256 与架构校验。\r\n"+
			"确认执行：/update apply force\r\n"+
			"替换期间本窗口会关闭并自动重启；若新版本 30 秒内没起来，脚本会自动回滚到 %s。\r\n"+
			"会话与配置保存在用户目录，不受影响。", uc.Version, core.Version))
		return
	}
	script, err := core.WriteApplyScript(dir, core.ApplyScript(exe, newExe, true))
	if err != nil {
		a.sysNote("写替换脚本失败：" + err.Error())
		return
	}
	if err := core.LaunchApply(script); err != nil {
		a.sysNote("启动替换脚本失败：" + err.Error() + "\r\n可手动运行：" + script)
		return
	}
	a.sysNote("正在替换并重启，稍等几秒。")
	if a.w != nil {
		a.w.Quit()
	}
}

// cmdUpdateRevert 回退到上一版。更新器保留的 .old 版本是这个功能的前提；
// 没了它（或者已经超过 30 天被自动删除）就提示用户并放弃。
func (a *App) cmdUpdateRevert(rest string) {
	_ = rest
	self, err := core.SelfExePath()
	if err != nil {
		a.sysNote("回退准备失败：拿不到自身路径。")
		return
	}
	info, err := core.PrepareRevertFrom(self)
	if err != nil {
		a.sysNote("回退准备失败：" + err.Error())
		return
	}
	var b strings.Builder
	b.WriteString("将回退到上一个版本：\r\n")
	fmt.Fprintf(&b, "  当前：%s\r\n", self)
	fmt.Fprintf(&b, "  回退副本：%s（%.1f MB，架构 %s，%s）\r\n\r\n",
		info.OldPath, float64(info.OldSize)/(1<<20), info.OldArch, info.OldMod.Format("01-02 15:04"))
	b.WriteString("执行前会关闭本窗口；回退脚本等本进程退出后替换文件并自动重启。\r\n")
	b.WriteString("若回退失败，脚本会自动恢复到当前版本，不会变砖。\r\n\r\n")
	b.WriteString("确定继续？")
	if !a.confirm("回退到上一个版本？", b.String()) {
		a.sysNote("已取消回退。")
		return
	}
	script, err := core.WriteApplyScript(core.UpdatesDir(), core.RevertScript(self, true))
	if err != nil {
		a.sysNote("写回退脚本失败：" + err.Error())
		return
	}
	if err := core.LaunchApply(script); err != nil {
		a.sysNote("启动回退脚本失败：" + err.Error() + "\r\n可手动运行：" + script)
		return
	}
	a.sysNote("正在回退并重启，稍等几秒。")
	if a.w != nil {
		a.w.Quit()
	}
}
