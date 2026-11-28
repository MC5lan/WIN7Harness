package app

import (
	"fmt"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/mcp"
)

// bootMCP 在后台连接配置里的 MCP 服务，并把它们提供的工具并入工具集。
// reload=true 时先断开全部旧连接再重连。
// 必须在 UI 线程调用：这里只取配置快照、更新状态，真正的连接/重连放后台，
// 结果通过 queueUI 回到 UI 线程，避免 goroutine 直接改 a.statusMsg/toast。
func (a *App) bootMCP(reload bool) {
	if a.cfg == nil {
		return
	}
	specs := a.cfg.Clone().MCPServers
	enabled := 0
	for _, s := range specs {
		if s.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		if reload {
			mcp.Shutdown()
			a.sysNote("没有启用的 MCP 服务（config.json 的 mcp_servers 中 enabled 为 true 的才会连接）")
		}
		return
	}
	a.statusMsg = "正在连接 MCP 服务…"
	a.ver++

	core.SafeGo("app/mcp.go:bootMCP", func() {
		if reload {
			mcp.Shutdown()
		}
		sts := mcp.Boot(specs)

		var okParts, failParts []string
		for _, st := range sts {
			if st.Err != "" {
				failParts = append(failParts, fmt.Sprintf("%s：%s", st.Name, st.Err))
				core.Logf("MCP %s 连接失败: %s", st.Name, st.Err)
				continue
			}
			okParts = append(okParts, fmt.Sprintf("%s（%d 个工具）", st.Name, st.Tools))
			core.Logf("MCP %s 已连接，工具 %d 个", st.Name, st.Tools)
		}

		var msg strings.Builder
		if len(okParts) > 0 {
			fmt.Fprintf(&msg, "MCP 已连接：%s", strings.Join(okParts, "、"))
		}
		if len(failParts) > 0 {
			if msg.Len() > 0 {
				msg.WriteString("\n")
			}
			fmt.Fprintf(&msg, "MCP 连接失败：%s", strings.Join(failParts, "、"))
		}

		a.queueUI(func() {
			a.statusMsg = ""
			if msg.Len() > 0 {
				a.sysNote(msg.String())
			}
			if len(failParts) > 0 {
				a.toast("部分 MCP 服务连接失败，详见对话", true)
			} else {
				a.toast("MCP 服务已就绪", false)
			}
			a.ver++
		})
	})
}
