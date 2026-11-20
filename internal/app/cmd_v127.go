package app

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/ui"
)

// v1.27 新增命令：/remote /loop /watch /update /font /compare
//
// 设计口径：能用一行自然语言说明白的，就不要做二级菜单。
// 这几条命令的共同要求是"打字就能用、打错能看懂"，所以每条都自带
// 无参调用时的现状回显，用户不需要先背语法。

const helpV127 = "" +
	"**远程与自动化（v1.27）**\n" +
	"- `/remote` 手机指挥台：`/remote on` 开启（局域网），`/remote code` 取 6 位配对码，`/remote off` 关闭\n" +
	"- `/loop` 定时任务：`/loop add 30m 提示词`、`/loop add daily 09:30 提示词`、`/loop list`、`/loop del 1`、`/loop off 1`\n" +
	"- `/watch` 目录守护：`/watch add C:\\proj 改动后跑测试`（可加 `--glob *.go,*.md`、`--ignore tmp`）、`/watch list`、`/watch del 1`\n" +
	"- `/update` 检查新版本：`/update` 查看，`/update check` 立即联网查一次；`/update apply` 下载并替换（需 force 确认）；`/update revert` 回退到上一版（更新器会保留 30 天）\n" +
	"- `/font 120` 内容字号缩放（80~200），`/theme hc` 高对比主题\n" +
	"- `/compare` 并列查看两个会话：`/compare` 列出，`/compare 3` 选定右侧会话，Esc 返回\n"

// —— /remote ——

func (a *App) cmdRemote(rest string) {
	st := a.v27
	if st == nil || st.rctl == nil {
		a.sysNote("手机指挥台未初始化。")
		return
	}
	arg := strings.ToLower(strings.TrimSpace(rest))
	switch arg {
	case "":
		// 无参打开扩展页的手机页签：开关、地址、配对码、待处理指令都在那儿，
		// 还要能直接点"关闭"。文字版留给 /remote status。
		a.page = PageExt
		a.extTab = extTabRemote
		return
	case "status", "状态":
		var b strings.Builder
		b.WriteString("手机远程指挥台 2.0\r\n")
		if st.rctl.Running() {
			b.WriteString("状态：已开启\r\n地址：" + a.remoteURL() + "\r\n")
			b.WriteString("监听范围：" + remoteScope(a.cfg.RemoteLAN) + "\r\n")
			b.WriteString(a.remoteSecurityLine() + "\r\n")
			b.WriteString("手机可看会话与输出：" + yesNo(a.cfg.RemoteEnabled) + "\r\n")
			b.WriteString("手机审批高危操作：" + yesNo(a.remoteApprovalOn()) + "\r\n")
			b.WriteString("扫码配对：/remote qr　配对码：/remote code\r\n")
		} else {
			b.WriteString("状态：已关闭（默认关闭，需手动开启）\r\n")
			b.WriteString("开启：/remote on（仅局域网） · 只在本机预览：/remote local\r\n")
		}
		b.WriteString("\r\n配对码 5 分钟有效、一次性使用。不开 HTTPS 时指令与配对码是明文过网，\r\n" +
			"同网段可被嗅探；需要加密用 /remote https on（手机首次要点一次「继续」）。\r\n" +
			"不做公网穿透，外网访问请自行配端口映射。")
		a.sysNote(b.String())

	case "on", "开", "开启", "lan":
		a.cfg.RemoteEnabled = true
		a.cfg.RemoteLAN = true
		a.cfg.Save()
		addr, err := a.startRemote()
		if err != nil {
			a.sysNote("开启失败：" + err.Error() + "\r\n如果提示端口被占用，可先 /remote off，或在配置里改 remote_port。")
			return
		}
		a.sysNote(fmt.Sprintf("指挥台已开启：%s\r\n手机连同一个 Wi-Fi，直接 /remote qr 扫码配对（也可手输配对码：%s）",
			a.remoteURLWithScheme(addr), st.rctl.PairCode()))

	case "local", "本机":
		a.cfg.RemoteEnabled = true
		a.cfg.RemoteLAN = false
		a.cfg.Save()
		a.stopRemote()
		addr, err := a.startRemote()
		if err != nil {
			a.sysNote("开启失败：" + err.Error())
			return
		}
		a.sysNote("指挥台已开启，仅本机可访问：" + addr + "\r\n配对码：" + st.rctl.PairCode())

	case "qr", "二维码", "扫码":
		a.showQR()

	case "https", "ssl", "加密":
		switch strings.ToLower(strings.TrimSpace(rest[len(fieldsFirst(rest)):])) {
		case "on", "开", "开启", "1":
			a.cfg.RemoteHTTPS = true
		case "off", "关", "关闭", "0":
			a.cfg.RemoteHTTPS = false
		case "":
			a.sysNote("当前：" + yesNo(a.cfg.RemoteHTTPS) + "\r\n用法：/remote https on|off")
			return
		default:
			a.sysNote("用法：/remote https on|off")
			return
		}
		wasOn := st.rctl.Running()
		a.cfg.Save()
		if wasOn {
			// 协议变了必须重开监听，否则手机还按老地址连就会一直超时。
			a.stopRemote()
			if _, err := a.startRemote(); err != nil {
				a.sysNote("已保存配置，但重新监听失败：" + err.Error())
				return
			}
		}
		if a.cfg.RemoteHTTPS {
			a.sysNote("已开启 HTTPS（自签证书）。\r\n手机第一次打开会提示证书不可信，选「继续访问」即可；" +
				"\r\n这是自签名证书的正常现象，不是被中间人攻击。\r\n已配对的手机需要重新扫码。")
		} else {
			a.sysNote("已关闭 HTTPS，回到明文 HTTP。同网段可被嗅探。")
		}

	case "approval", "approve", "审批":
		switch strings.ToLower(strings.TrimSpace(rest[len(fieldsFirst(rest)):])) {
		case "on", "开", "开启", "1":
			a.setRemoteApproval(true)
		case "off", "关", "关闭", "0":
			a.setRemoteApproval(false)
		case "":
			a.sysNote("手机审批：" + yesNo(a.remoteApprovalOn()) + "\r\n用法：/remote approval on|off")
			return
		default:
			a.sysNote("用法：/remote approval on|off")
			return
		}
		if a.remoteApprovalOn() {
			a.sysNote("已开启：高危操作会同时推到手机，手机点了就算答复，两边谁先点谁生效。")
		} else {
			a.sysNote("已关闭：高危操作只在电脑上确认。")
		}

	case "code", "配对码":
		if !st.rctl.Running() {
			a.sysNote("指挥台还没开。先 /remote on")
			return
		}
		a.sysNote(fmt.Sprintf("配对码：%s\r\n5 分钟内有效，配对成功后立即作废。\r\n地址：%s",
			st.rctl.PairCode(), a.remoteURL()))

	case "off", "关", "关闭":
		a.cfg.RemoteEnabled = false
		a.stopRemote()
		a.cfg.Save()
		a.sysNote("指挥台已关闭，监听已停止。")

	default:
		a.toast("用法：/remote [on|off|code|qr|local|https|approval]", true)
	}
}

// remoteURL 拼给用户看的地址；监听在 0.0.0.0 时换成局域网 IP。
func (a *App) remoteURL() string {
	addr := a.v27.rctl.Addr()
	if addr == "" {
		return "-"
	}
	if strings.Contains(addr, "://") {
		return addr
	}
	return a.remoteScheme() + "://" + addr + "/"
}

func remoteScope(lan bool) string {
	if lan {
		return "局域网可访问（同网段设备都能打开配对页，仍需要配对码）"
	}
	return "仅本机 127.0.0.1"
}

// —— /loop 定时任务 ——

func (a *App) cmdLoop(rest string) {
	st := a.v27
	if st == nil {
		a.sysNote("定时任务未初始化。")
		return
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		a.page = PageAuto
		a.autoTab = autoTabSched
		return
	}
	switch strings.ToLower(fields[0]) {
	case "list", "ls", "列", "列表":
		a.listLoops()

	case "add", "新增", "加":
		spec, prompt, err := splitScheduleArgs(strings.TrimSpace(rest[len(fields[0]):]))
		if err != nil {
			a.sysNote("用法：/loop add 30m 提示词\r\n      /loop add daily 09:30 提示词\r\n      /loop add weekly mon 09:00 提示词\r\n" + err.Error())
			return
		}
		st.schedMu.Lock()
		sc, err := st.sched.Add(spec, prompt, a.convWorkDir(), time.Now())
		if err == nil {
			err = st.sched.Save()
		}
		st.schedMu.Unlock()
		if err != nil {
			a.sysNote("添加失败：" + err.Error())
			return
		}
		a.sysNote(fmt.Sprintf("已添加定时任务 #%d：%s\r\n触发时在当前会话执行，界面需保持运行。", a.loopIndex(sc.ID), sc.Describe()))

	case "del", "rm", "delete", "删", "删除":
		if len(fields) < 2 {
			a.sysNote("用法：/loop del 序号（序号见 /loop list）")
			return
		}
		id, ok := a.loopIDByIndex(fields[1])
		if !ok {
			a.sysNote("没有这个序号：" + fields[1])
			return
		}
		st.schedMu.Lock()
		removed := st.sched.Remove(id)
		if removed {
			_ = st.sched.Save()
		}
		st.schedMu.Unlock()
		if removed {
			a.sysNote("已删除定时任务 #" + fields[1])
		} else {
			a.sysNote("删除失败：任务不存在")
		}

	case "on", "off", "开", "关":
		if len(fields) < 2 {
			a.sysNote("用法：/loop on|off 序号")
			return
		}
		on := strings.ToLower(fields[0]) == "on" || fields[0] == "开"
		id, ok := a.loopIDByIndex(fields[1])
		if !ok {
			a.sysNote("没有这个序号：" + fields[1])
			return
		}
		st.schedMu.Lock()
		sc, found := st.sched.SetEnabled(id, on, time.Now())
		if found {
			_ = st.sched.Save()
		}
		st.schedMu.Unlock()
		if !found {
			a.sysNote("任务不存在")
			return
		}
		state := "已启用"
		if !on {
			state = "已暂停"
		}
		a.sysNote(fmt.Sprintf("%s：%s\r\n下次触发：%s", state, sc.Describe(), fmtRunAt(sc.NextRun)))

	default:
		// 允许 /loop 30m 提示词 这种省略 add 的写法：少打一个词也是省事
		spec, prompt, err := splitScheduleArgs(rest)
		if err != nil {
			a.toast("用法：/loop add 30m 提示词", true)
			return
		}
		a.cmdLoop("add " + spec + " " + prompt)
	}
}

func (a *App) listLoops() {
	st := a.v27
	st.schedMu.Lock()
	items := append([]*core.Schedule(nil), st.sched.Items...)
	st.schedMu.Unlock()
	if len(items) == 0 {
		a.sysNote("还没有定时任务。\r\n添加：/loop add 30m 检查一次构建结果\r\n      /loop add daily 09:30 汇总昨天的代码改动")
		return
	}
	var b strings.Builder
	b.WriteString("定时任务（触发时在当前会话执行）：\r\n")
	now := time.Now()
	for i, s := range items {
		state := "启用"
		if !s.Enabled {
			state = "暂停"
		}
		next := "—"
		if s.Enabled {
			next = fmtRunAt(s.NextRun)
		}
		_ = now
		b.WriteString(fmt.Sprintf("%d. [%s] %s｜%s｜已跑 %d 次｜下次 %s\r\n   提示词：%s\r\n",
			i+1, state, s.Describe(), "", s.Runs, next, clipRunes(s.Prompt, 80)))
	}
	b.WriteString("\r\n删除：/loop del 1 · 暂停：/loop off 1")
	a.sysNote(b.String())
}

// splitScheduleArgs 把 "daily 09:30 提示词" 拆成排期与提示词。
// 排期最长两个 token（daily 09:30 / weekly mon 09:00），其余全是提示词。
func splitScheduleArgs(s string) (string, string, error) {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) < 2 {
		return "", "", fmt.Errorf("缺少提示词")
	}
	n := 1
	switch strings.ToLower(fields[0]) {
	case "daily", "每天", "weekly", "每周", "every", "每":
		n = 2
	}
	if len(fields) <= n {
		return "", "", fmt.Errorf("缺少提示词")
	}
	return strings.Join(fields[:n], " "), strings.Join(fields[n:], " "), nil
}

func (a *App) loopIndex(id string) int {
	st := a.v27
	st.schedMu.Lock()
	defer st.schedMu.Unlock()
	for i, s := range st.sched.Items {
		if s.ID == id {
			return i + 1
		}
	}
	return 0
}

func (a *App) loopIDByIndex(arg string) (string, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil || n <= 0 {
		return "", false
	}
	st := a.v27
	st.schedMu.Lock()
	defer st.schedMu.Unlock()
	if n > len(st.sched.Items) {
		return "", false
	}
	return st.sched.Items[n-1].ID, true
}

func fmtRunAt(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Until(t)
	if d < 0 {
		return "即将触发"
	}
	if d < time.Minute {
		return fmt.Sprintf("%d 秒后", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%d 分钟后", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return t.Format("15:04") + fmt.Sprintf("（%d 小时后）", int(d.Hours()))
	}
	return t.Format("01-02 15:04")
}

// —— /watch 目录守护 ——

func (a *App) cmdWatch(rest string) {
	st := a.v27
	if st == nil {
		a.sysNote("目录守护未初始化。")
		return
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		a.page = PageAuto
		a.autoTab = autoTabWatch
		return
	}
	switch strings.ToLower(fields[0]) {
	case "list", "ls", "列表":
		a.listWatches()

	case "add", "新增":
		path, prompt, globs, ignores, err := parseWatchArgs(strings.TrimSpace(rest[len(fields[0]):]))
		if err != nil {
			a.sysNote("用法：/watch add C:\\proj 有改动就总结一下\r\n" +
				"      /watch add C:\\proj --glob *.go,*.md 改动后跑测试\r\n" +
				"\r\n" + err.Error())
			return
		}
		st.watchMu.Lock()
		r, err := st.watch.Add(path, prompt, globs, ignores, time.Now())
		if err == nil {
			err = st.watch.Save()
		}
		st.watchMu.Unlock()
		if err != nil {
			a.sysNote("添加失败：" + err.Error())
			return
		}
		a.sysNote(fmt.Sprintf("已守护：%s\r\n触发条件：%s\r\n触发后执行：%s\r\n\r\n"+
			"第一次触发需要先建立基线：现已记录当前文件状态，之后的变化才会触发。",
			r.Path, watchMatchDesc(r), clipRunes(r.Prompt, 100)))

	case "gap", "防抖", "静默":
		if len(fields) < 3 {
			a.sysNote("用法：/watch gap 序号 秒数\r\n例：/watch gap 1 60（同一规则两次触发至少隔 60 秒）\r\n设为 0 表示只在内容停止变化后立刻触发。")
			return
		}
		id, ok := a.watchIDByIndex(fields[1])
		if !ok {
			a.sysNote("没有这个序号：" + fields[1])
			return
		}
		var sec int
		if _, err := fmt.Sscanf(fields[2], "%d", &sec); err != nil || sec < 0 {
			a.sysNote("秒数要填 0 或正整数。")
			return
		}
		st.watchMu.Lock()
		var found bool
		var r *core.WatchRule
		for _, it := range st.watch.Items {
			if it.ID == id {
				it.MinGapSec = sec
				found, r = true, it
				break
			}
		}
		if found {
			_ = st.watch.Save()
		}
		st.watchMu.Unlock()
		if !found {
			a.sysNote("规则不存在。")
			return
		}
		if sec == 0 {
			a.sysNote(fmt.Sprintf("已取消防抖窗口：%s\r\n内容停止变化后立即触发。连续改动时可能触发得比较频繁。", r.Path))
		} else {
			a.sysNote(fmt.Sprintf("已设置防抖窗口 %d 秒：%s\r\n窗口内的改动会合并成一次触发，不会丢。", sec, r.Path))
		}

	case "del", "rm", "删":
		if len(fields) < 2 {
			a.sysNote("用法：/watch del 序号")
			return
		}
		id, ok := a.watchIDByIndex(fields[1])
		if !ok {
			a.sysNote("没有这个序号：" + fields[1])
			return
		}
		st.watchMu.Lock()
		removed := st.watch.Remove(id)
		if removed {
			_ = st.watch.Save()
		}
		st.watchMu.Unlock()
		if removed {
			a.sysNote("已删除守护规则 #" + fields[1])
		}

	case "on", "off", "开", "关":
		if len(fields) < 2 {
			a.sysNote("用法：/watch on|off 序号")
			return
		}
		on := strings.ToLower(fields[0]) == "on" || fields[0] == "开"
		id, ok := a.watchIDByIndex(fields[1])
		if !ok {
			a.sysNote("没有这个序号：" + fields[1])
			return
		}
		st.watchMu.Lock()
		r, found := st.watch.SetEnabled(id, on, time.Now())
		if found {
			_ = st.watch.Save()
		}
		st.watchMu.Unlock()
		if found {
			state := "已启用"
			if !on {
				state = "已暂停"
			}
			a.sysNote(state + "：" + r.Path)
		}

	default:
		a.toast("用法：/watch add 路径 提示词", true)
	}
}

func (a *App) listWatches() {
	st := a.v27
	st.watchMu.Lock()
	items := append([]*core.WatchRule(nil), st.watch.Items...)
	st.watchMu.Unlock()
	if len(items) == 0 {
		a.sysNote("还没有守护规则。\r\n添加：/watch add C:\\proj 有改动就总结一下\r\n" +
			"说明：每 15 秒比对一次文件快照，有新增/修改/删除才触发，不常驻读文件。")
		return
	}
	var b strings.Builder
	b.WriteString("目录守护（每 15 秒比对一次快照）：\r\n")
	for i, r := range items {
		state := "启用"
		if !r.Enabled {
			state = "暂停"
		}
		last := "从未触发"
		if !r.LastFire.IsZero() {
			last = r.LastFire.Format("01-02 15:04")
		}
		b.WriteString(fmt.Sprintf("%d. [%s] %s｜%s｜静默 %ds｜已触发 %d 次｜最近 %s\r\n   触发后：%s\r\n",
			i+1, state, r.Path, watchMatchDesc(r), watchGap(r), r.Fires, last, clipRunes(r.Prompt, 80)))
	}
	b.WriteString("\r\n删除：/watch del 1 · 暂停：/watch off 1 · 防抖窗口：/watch gap 1 60")
	a.sysNote(b.String())
}

// parseWatchArgs 解析 /watch add 的参数：路径 + 可选 --glob/--ignore + 提示词。
func parseWatchArgs(s string) (path, prompt string, globs, ignores []string, err error) {
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return "", "", nil, nil, fmt.Errorf("缺少路径或提示词")
	}
	path = fields[0]
	var promptParts []string
	for i := 1; i < len(fields); i++ {
		switch strings.ToLower(fields[i]) {
		case "--glob", "--ignore":
			key := strings.ToLower(fields[i])
			if i+1 >= len(fields) {
				return "", "", nil, nil, fmt.Errorf("%s 后面要跟匹配式", key)
			}
			list := splitList(fields[i+1])
			if key == "--glob" {
				globs = append(globs, list...)
			} else {
				ignores = append(ignores, list...)
			}
			i++
		default:
			promptParts = append(promptParts, fields[i])
		}
	}
	if len(promptParts) == 0 {
		return "", "", nil, nil, fmt.Errorf("缺少提示词")
	}
	return path, strings.Join(promptParts, " "), globs, ignores, nil
}

func splitList(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func watchMatchDesc(r *core.WatchRule) string {
	if len(r.Patterns) == 0 {
		return "全部文件"
	}
	return strings.Join(r.Patterns, ", ")
}

func (a *App) watchIDByIndex(arg string) (string, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil || n <= 0 {
		return "", false
	}
	st := a.v27
	st.watchMu.Lock()
	defer st.watchMu.Unlock()
	if n > len(st.watch.Items) {
		return "", false
	}
	return st.watch.Items[n-1].ID, true
}

// —— /update ——

func (a *App) cmdUpdate(rest string) {
	url := strings.TrimSpace(a.cfg.UpdateURL)
	if url == "" {
		url = core.DefaultManifestURL
	}
	arg := strings.ToLower(strings.TrimSpace(rest))
	if arg != "" && arg != "check" && arg != "查" {
		a.toast("用法：/update 或 /update check", true)
		return
	}
	a.sysNote("正在检查更新：" + url)
	core.SafeGo("app/cmd_v127.go:601", func() {
		m, newer, err := core.CheckUpdate(core.Version, url, core.HTTPFetcher)
		var msg string
		switch {
		case err != nil:
			msg = "检查失败：" + err.Error() + "\r\n" +
				"常见原因：没有网络、清单地址写错、公司代理拦了 HTTPS。\r\n" +
				"可先跑 /netdiag 看卡在哪一层，或用 /remote 之外的机器上手动确认地址。"
		case !newer:
			msg = fmt.Sprintf("已是最新版本（当前 %s）。", core.Version)
		default:
			f, _ := m.FileFor(runtime.GOARCH)
			msg = fmt.Sprintf("发现新版本 %s（当前 %s）\r\n%s\r\n", m.Version, core.Version, m.ShortNotes())
			if f.URL != "" {
				msg += "\r\n下载地址：" + f.URL
			}
			if m.PageURL != "" {
				msg += "\r\n发布页：" + m.PageURL
			}
			msg += "\r\n\r\n本程序不会自动替换自身：请手动下载后覆盖 exe（配置与会话保存在用户目录，不会被覆盖）。"
		}
		if a.v27 != nil {
			select {
			case a.v27.updCh <- msg:
			default:
			}
		}
	})
}

// —— /font 字号缩放 ——

func (a *App) cmdFont(rest string) {
	arg := strings.TrimSpace(rest)
	if arg == "" {
		a.sysNote(fmt.Sprintf("当前内容字号：%d%%\r\n调整：/font 120（范围 80~200）\r\n"+
			"只放大对话内容（含代码块），工具栏与按钮保持原尺寸，避免文字顶出行框。", a.cfg.FontScale))
		return
	}
	n, err := strconv.Atoi(strings.TrimSuffix(arg, "%"))
	if err != nil || n < 80 || n > 200 {
		a.toast("字号范围 80~200，例如 /font 120", true)
		return
	}
	a.cfg.FontScale = n
	a.layKey = layKey{}
	a.cfg.Save()
	a.ver++
	a.sysNote(fmt.Sprintf("内容字号已设为 %d%%。", n))
}

// —— /theme 主题（含高对比） ——

func (a *App) cmdTheme(rest string) {
	arg := strings.ToLower(strings.TrimSpace(rest))
	switch arg {
	case "":
		a.sysNote("当前主题：" + a.cfg.Theme + "\r\n切换：/theme dark · /theme light · /theme hc（高对比）")
	case "dark", "light", "hc", "high-contrast", "contrast":
		a.cfg.Theme = arg
		a.cfg.Normalize()
		a.th = ui.ResolveTheme(a.cfg.Theme)
		a.cfg.Save()
		a.ver++
		a.sysNote("主题已切换：" + a.cfg.Theme)
	default:
		a.toast("可用主题：dark / light / hc", true)
	}
}

// fieldsFirst 取首个词，用于切掉子命令后继续解析参数。
func fieldsFirst(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

func yesNo(b bool) string {
	if b {
		return "开"
	}
	return "关"
}

// remoteURLWithScheme 在没有 Server 地址时也能拼出可用地址（启动瞬间用）。
func (a *App) remoteURLWithScheme(addr string) string {
	if strings.Contains(addr, "://") {
		return addr
	}
	return a.remoteScheme() + "://" + addr + "/"
}

// watchGap 取规则的有效静默窗口（秒）。0 表示未设置过，按默认 30 秒算，
// 显示的必须是真正生效的值，否则用户会以为没防抖。
func watchGap(r *core.WatchRule) int {
	if r.MinGapSec > 0 {
		return r.MinGapSec
	}
	return 30
}
