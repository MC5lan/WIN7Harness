package app

import (
	"fmt"
	"strings"

	"win7harness/internal/agent"
	"win7harness/internal/core"
	"win7harness/internal/mcp"
)

// /mcp add：MCP 一键安装向导。
//
// 设计取舍：不做多步表单，而是"列表 → 一条命令带参数"。
// 多步表单在终端风格的聊天界面里维护成本高、打断感强；
// 一条 `/mcp add 5 token=ghp_xxx` 既好复制给同事，也好写进文档。
// 安装前后台试连，成功才写配置——失败的半成品配置比没有配置更麻烦。

// cmdMCPAdd 处理 /mcp add 及其子参数。
func (a *App) cmdMCPAdd(rest string) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		a.sysNote(a.mcpCatalogText())
		return
	}
	fields := strings.Fields(rest)
	sel := fields[0]
	tpl, ok := mcp.FindTemplate(sel)
	if !ok {
		a.sysNote("没有这个模板：" + sel + "\n\n" + a.mcpCatalogText())
		return
	}
	if a.cfg == nil {
		return
	}
	if mcp.Installed(a.cfg.MCPServers, tpl.ID) {
		a.sysNote(fmt.Sprintf("%s 已经装过了。要重装请先在 /mcp 里删除，或直接改 config.json。", tpl.Name))
		return
	}
	vals := map[string]string{}
	for _, f := range fields[1:] {
		kv := strings.SplitN(f, "=", 2)
		if len(kv) != 2 {
			a.sysNote("参数格式应为 `键=值`，例如 `/mcp add 1 dir=C:\\proj`")
			return
		}
		vals[strings.ToLower(strings.TrimSpace(kv[0]))] = strings.TrimSpace(kv[1])
	}
	spec, err := tpl.Build(vals)
	if err != nil {
		a.sysNote("还差一步：" + err.Error() + "\n\n用法：`/mcp add " + tpl.ID + " " + usageOf(tpl) + "`")
		return
	}
	a.sysNote(fmt.Sprintf("正在试连 %s…（首次会下载依赖，可能慢一点）", tpl.Name))
	// 试连会拉起子进程，放后台做，避免界面卡住。
	core.SafeGo("app/mcpwizard.go:56", func() {
		n, perr := mcp.Probe(spec, 30)
		if perr != nil {
			msg := fmt.Sprintf("%s 试连失败：%v", tpl.Name, perr)
			if tpl.Needs != "" {
				msg += "\n前置条件：" + tpl.Needs
			}
			if tpl.Net {
				msg += "\n若本机离线，请先在联网环境装好依赖。"
			}
			a.queueUI(func() {
				a.evCh <- agent.Event{Kind: "notice", Text: msg}
			})
			return
		}
		a.queueUI(func() {
			if a.cfg == nil {
				return
			}
			a.cfg.MCPServers = append(a.cfg.MCPServers, spec)
			if err := a.cfg.Save(); err != nil {
				a.evCh <- agent.Event{Kind: "notice", Text: "服务连通了，但配置保存失败：" + err.Error()}
				return
			}
			// 已连上的这个进程不能直接接管，统一走一次重连，状态才不会分裂。
			a.bootMCP(true)
			a.evCh <- agent.Event{Kind: "notice", Text: fmt.Sprintf(
				"%s 安装成功：探测到 %d 个工具，已写入配置并重新连接。\n工具名形如 `mcp_%s_工具名`，可以直接让我调用。",
				tpl.Name, n, tpl.ID)}
		})
	})
}

// mcpCatalogText 生成模板清单文本。
func (a *App) mcpCatalogText() string {
	var b strings.Builder
	b.WriteString("**可一键安装的 MCP 服务**\n\n")
	b.WriteString("| 序号 | 服务 | 说明 | 前置 |\n|---|---|---|---|\n")
	var installed []core.MCPServer
	if a.cfg != nil {
		installed = a.cfg.MCPServers
	}
	for i, t := range mcp.Catalog() {
		mark := t.Name
		if mcp.Installed(installed, t.ID) {
			mark += "（已装）"
		}
		fmt.Fprintf(&b, "| %d | %s | %s | %s |\n", i+1, mark, t.Desc, t.Needs)
	}
	b.WriteString("\n安装：`/mcp add 序号 参数=值`，例如 `/mcp add 1 dir=C:\\proj`。")
	b.WriteString("\n只输入 `/mcp add 序号` 时会告诉你还缺哪些参数。")
	return b.String()
}

// usageOf 生成某模板的参数示例。
func usageOf(t mcp.Template) string {
	var parts []string
	for _, p := range t.Params {
		key := p.Key
		if p.Sensitive {
			parts = append(parts, key+"=<"+p.Prompt+">")
			continue
		}
		parts = append(parts, key+"=...")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}
