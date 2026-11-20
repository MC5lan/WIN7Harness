package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"win7harness/internal/core"
	"win7harness/internal/coreutil"
	"win7harness/internal/tools"
)

// v1.26 新增命令：会话分叉、审批策略、自定义子代理清单、网络诊断。

// cmdFork 把当前会话整段复制成一个新会话，并切过去继续聊。
// 用途：一个会话里试了 A 方案，想回到同一出发点再试 B，不用重新交代背景。
func (a *App) cmdFork(rest string) {
	cur := a.currentConv()
	if cur == nil {
		a.toast("当前没有可分叉的会话", true)
		return
	}
	a.saveConv()

	msgs := make([]Message, 0, len(cur.Msgs))
	for _, m := range cur.Msgs {
		cp := m
		// 图片切片共享底层数组会互相影响，这里按值复制。
		if len(m.Images) > 0 {
			cp.Images = append([]string{}, m.Images...)
		}
		msgs = append(msgs, cp)
	}

	title := coreutil.ForkTitle(cur.Title, rest)

	cp := &Conv{
		ID:      newConvID(),
		Title:   title,
		Msgs:    msgs,
		At:      time.Now(),
		Tok:     cur.Tok,
		Model:   cur.Model,
		Prov:    cur.Prov,
		WorkDir: cur.WorkDir,
		Loaded:  true,
	}
	a.convs = append([]*Conv{cp}, a.convs...)
	a.active = 0
	a.loadConv()
	a.saveConv()
	a.sysNote(fmt.Sprintf("已从「%s」分叉出新会话（%d 条历史），后续改动只影响新会话。", cur.Title, len(msgs)))
}

// cmdPerm 审批策略：查看 / 切模式 / 加规则 / 删规则 / 试判。
func (a *App) cmdPerm(rest string) {
	if a.eng == nil || a.eng.Policy == nil {
		a.sysNote("审批策略尚未初始化（当前运行在命令行精简模式）。")
		return
	}
	pol := a.eng.Policy
	f := strings.Fields(rest)
	if len(f) == 0 {
		a.sysNote(pol.Summary() + "\n\n用法：\n" +
			"- `/perm ask|auto|plan` 切换模式（ask 写操作逐次确认 / auto 非高危放行 / plan 只读）\n" +
			"- `/perm add allow run_command:git *` 加规则（类型 allow/deny/ask）\n" +
			"- `/perm del 2` 删除第 2 条规则\n" +
			"- `/perm test run_command git status` 预演一次判定")
		return
	}
	switch strings.ToLower(f[0]) {
	case "ask", "auto", "plan":
		pol.Mode = strings.ToLower(f[0])
		a.savePolicy()
		a.sysNote("审批模式已切换为 " + pol.Mode + "，立即生效。\n\n" + pol.Summary())

	case "add":
		if len(f) < 3 {
			a.toast("用法：/perm add allow run_command:git *", true)
			return
		}
		kind := f[1]
		rule := strings.Join(f[2:], " ")
		got, err := pol.Add(kind, rule)
		if err != nil {
			a.toast("加规则失败："+err.Error(), true)
			return
		}
		a.savePolicy()
		a.sysNote("已添加 " + kind + " 规则：" + got + "\n\n" + pol.Summary())

	case "del", "rm", "remove":
		if len(f) < 2 {
			a.toast("用法：/perm del 2", true)
			return
		}
		n, err := strconv.Atoi(f[1])
		if err != nil {
			a.toast("序号要填数字", true)
			return
		}
		got, ok := pol.Remove(n)
		if !ok {
			a.toast(fmt.Sprintf("没有第 %d 条规则", n), true)
			return
		}
		a.savePolicy()
		a.sysNote("已删除规则：" + got + "\n\n" + pol.Summary())

	case "test", "dry":
		if len(f) < 3 {
			a.toast("用法：/perm test run_command git status", true)
			return
		}
		tool := f[1]
		target := strings.Join(f[2:], " ")
		// 与真实调用保持一致：只有 run_command 才按命令内容分级，
		// 其余工具都是 RiskSafe。过去这里硬编码 RiskDanger，
		// 预演结果永远是"高危命令需逐次确认"，排障时会被误导。
		danger := core.RiskSafe
		if strings.EqualFold(tool, "run_command") {
			danger = core.CommandRisk(target)
		}
		d := pol.Decide(tool, target, true, danger, a.eng.WorkDir)
		a.sysNote(fmt.Sprintf("判定：%s\n工具：%s\n目标：%s\n理由：%s", d.Kind, tool, target, d.Reason))

	default:
		a.toast("未知子命令，直接输入 /perm 看用法", true)
	}
}

// savePolicy 把内存里的策略写回配置（模式 + 扁平规则），保证重启后仍在。
func (a *App) savePolicy() {
	if a.cfg == nil || a.eng == nil || a.eng.Policy == nil {
		return
	}
	a.cfg.PermMode = a.eng.Policy.Mode
	a.cfg.PermRules = a.eng.Policy.FlatRules()
	if err := a.cfg.Save(); err != nil {
		a.toast("策略生效但保存失败："+err.Error(), true)
	}
}

// cmdAgents 列出自定义子代理（.harness/agents/*.md）。
func (a *App) cmdAgents(rest string) {
	wd := ""
	if a.eng != nil {
		wd = a.eng.WorkDir
	}
	if strings.TrimSpace(rest) == "" {
		a.page = PageExt
		a.extTab = extTabAgents
		return
	}
	defs, warn := core.LoadAgentDefs(wd)
	var b strings.Builder
	b.WriteString("自定义子代理目录：" + filepath.Join(wd, ".harness", "agents") + "\n")
	if warn != "" {
		b.WriteString("注意：" + warn + "\n")
	}
	if len(defs) == 0 {
		b.WriteString("\n（暂无）新建一个 .md，头部写 name / description / tools / model 即可；" +
			"然后 task 工具的 agent 参数就能指定它。")
		a.sysNote(b.String())
		return
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	fmt.Fprintf(&b, "\n共 %d 个：\n", len(defs))
	for _, d := range defs {
		fmt.Fprintf(&b, "\n- %s：%s", d.Name, d.Description)
		if len(d.Tools) > 0 {
			fmt.Fprintf(&b, "\n  工具限制：%s", strings.Join(d.Tools, ", "))
		}
		if d.Model != "" {
			fmt.Fprintf(&b, "\n  模型：%s", d.Model)
		}
	}
	a.sysNote(b.String())
}

// cmdNetDiag 分层诊断模型端点连通性：DNS → TCP → TLS → HTTP。
func (a *App) cmdNetDiag(rest string) {
	target := strings.TrimSpace(rest)
	if target == "" && a.cfg != nil {
		p := a.cfg.Provider()
		target = p.BaseURL
	}
	if target == "" {
		a.toast("没有可诊断的地址：/netdiag https://api.example.com", true)
		return
	}
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = "https://" + target
	}
	proxyDesc := "系统/环境变量"
	if a.cfg != nil {
		switch strings.ToLower(strings.TrimSpace(a.cfg.ProxyMode)) {
		case "direct", "off", "none":
			proxyDesc = "直连（已关闭代理）"
		case "manual":
			proxyDesc = "手动 " + a.cfg.ProxyURL
		}
	}
	res := core.Diagnose(target, proxyDesc, 15*time.Second)
	a.sysNote(res.String())
	core.Logf("netdiag %s: %s", target, res.Verdict)
}

// ocrStatus 给 /status 用：说明本机 OCR 是否可用。
func ocrStatus() string {
	if _, p := ocrEngine(); p != "" {
		return "可用（" + p + "）"
	}
	return "未安装（read_image 仍可直接看图，OCR 需 tesseract）"
}

// ocrEngine 桥接 tools 包的查找函数，避免 app 直接依赖 exec 细节。
func ocrEngine() (string, string) {
	return tools.OCREngineName()
}

// permLine 给状态页一行话概括当前审批策略。
func permLine(cfg *core.Config) string {
	if cfg == nil {
		return "未初始化"
	}
	mode := cfg.PermMode
	if mode == "" {
		mode = "ask（默认）"
	}
	return fmt.Sprintf("%s · 规则 %d 条", mode, len(cfg.PermRules))
}

// permDirs 白名单目录（含默认项），供状态页展示。
func permDirs(cfg *core.Config) []string {
	if cfg == nil {
		return nil
	}
	return cfg.PermDirs
}
