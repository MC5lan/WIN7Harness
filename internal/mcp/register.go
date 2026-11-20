package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"win7harness/internal/core"
	"win7harness/internal/tools"
)

// Status 一个服务连接后的状态摘要。
type Status struct {
	Name  string
	Tools int
	Alive bool
	Err   string
}

var (
	mu      sync.Mutex
	clients = map[string]*Client{}
	// 工具名 -> 服务名，便于卸载与展示
	owned = map[string]string{}
)

// Boot 按配置连接全部启用的 MCP 服务，并把它们的工具并入工具集。
// 返回每个服务的状态行；单个服务失败不影响其它服务。
func Boot(specs []core.MCPServer) []Status {
	var out []Status
	for _, spec := range specs {
		if !spec.Enabled {
			continue
		}
		n, err := startOne(spec)
		st := Status{Name: spec.Name, Tools: n}
		if err != nil {
			st.Err = err.Error()
		} else {
			st.Alive = true
		}
		out = append(out, st)
	}
	return out
}

func startOne(spec core.MCPServer) (int, error) {
	c, err := Start(spec)
	if err != nil {
		return 0, err
	}
	list, err := c.ListTools()
	if err != nil {
		c.Close()
		return 0, err
	}

	mu.Lock()
	if old, ok := clients[spec.Name]; ok {
		old.Close()
		for name, owner := range owned {
			if owner == spec.Name {
				tools.Unregister(name)
				delete(owned, name)
			}
		}
	}
	clients[spec.Name] = c
	mu.Unlock()

	count := 0
	for _, info := range list {
		toolName := toolNameFor(spec.Name, info.Name)
		schema := string(info.InputSchema)
		if strings.TrimSpace(schema) == "" || schema == "null" {
			schema = `{"type":"object","properties":{}}`
		}
		if !json.Valid([]byte(schema)) {
			continue
		}
		mcpName := info.Name
		server := spec.Name
		desc := info.Description
		if strings.TrimSpace(desc) == "" {
			desc = "MCP 工具 " + mcpName
		}
		desc = fmt.Sprintf("[MCP:%s] %s", server, desc)
		tools.RegisterDynamic(tools.Tool{
			Name:   toolName,
			Desc:   desc,
			Schema: schema,
			Run: func(ctx *tools.Context, args map[string]interface{}) (tools.Result, error) {
				cli := findClient(server)
				if cli == nil {
					return tools.Result{}, fmt.Errorf("MCP 服务 %s 未连接", server)
				}
				if ctx != nil && ctx.Progress != nil {
					ctx.Progress("MCP 调用：" + server + "/" + mcpName)
				}
				out, err := cli.CallTool(mcpName, args)
				if err != nil {
					return tools.Result{}, err
				}
				if strings.TrimSpace(out) == "" {
					out = "（无输出）"
				}
				return tools.Result{Output: out}, nil
			},
		})
		mu.Lock()
		owned[toolName] = server
		mu.Unlock()
		count++
	}
	return count, nil
}

func findClient(name string) *Client {
	mu.Lock()
	defer mu.Unlock()
	return clients[name]
}

// toolName 把服务名与工具名拼成合法的 function name（只允许字母数字下划线短横，长度 ≤64）。
func toolNameFor(server, tool string) string {
	clean := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
				b.WriteRune(r)
			case r == '.' || r == '/' || r == ' ':
				b.WriteByte('_')
			}
		}
		return b.String()
	}
	name := "mcp_" + clean(server) + "_" + clean(tool)
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// Statuses 返回当前已连接服务状态。
func Statuses() []Status {
	mu.Lock()
	defer mu.Unlock()
	names := make([]string, 0, len(clients))
	for n := range clients {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Status, 0, len(names))
	for _, n := range names {
		c := clients[n]
		count := 0
		for _, owner := range owned {
			if owner == n {
				count++
			}
		}
		out = append(out, Status{Name: n, Tools: count, Alive: c.Alive()})
	}
	return out
}

// Shutdown 关闭全部连接并注销工具。
func Shutdown() {
	mu.Lock()
	cs := make([]*Client, 0, len(clients))
	for _, c := range clients {
		cs = append(cs, c)
	}
	names := make([]string, 0, len(owned))
	for n := range owned {
		names = append(names, n)
	}
	clients = map[string]*Client{}
	owned = map[string]string{}
	mu.Unlock()

	for _, c := range cs {
		c.Close()
	}
	for _, n := range names {
		tools.Unregister(n)
	}
}
