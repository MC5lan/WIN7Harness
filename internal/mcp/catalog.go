package mcp

import (
	"fmt"
	"sort"
	"strings"

	"win7harness/internal/core"
)

// MCP 一键安装：内置常见服务的安装模板，用户按序号选、按提示填参数，
// 程序负责拼命令、试连通、写配置。
//
// 为什么要做这件事：手写 config.json 里的 command/args/env 是最容易出错的一步——
// 路径里的空格忘了引号、npx 少个 -y 卡在交互提示、token 写进 args 被进程列表看到。
// 模板把这些都固化下来，用户只需要回答"装哪个、目录是哪个、key 是多少"。

// Param 是模板里的一个待填参数。
type Param struct {
	Key       string // 占位名，如 dir / token / db
	Prompt    string // 提示语
	Required  bool
	Env       bool // true 填进环境变量（敏感值走这里，不出现在命令行里）
	Sensitive bool
}

// Template 是一个可一键安装的 MCP 服务模板。
type Template struct {
	ID      string
	Name    string
	Desc    string
	Command string
	Args    []string // 可含 {dir} {db} 这类占位
	EnvKeys []string // 需要填值的环境变量名（与 Params 中 Env=true 的项对应）
	Params  []Param
	// Needs 说明这台机器需要什么前置条件，安装失败时一并提示。
	Needs string
	// Net 表示安装需要联网下载（离线环境会失败）。
	Net bool
}

// Catalog 返回内置模板清单（顺序固定，便于按序号选择）。
func Catalog() []Template {
	return []Template{
		{
			ID: "filesystem", Name: "文件系统",
			Desc:    "在指定目录内读写文件，比内置文件工具更细粒度",
			Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-filesystem", "{dir}"},
			Params: []Param{{Key: "dir", Prompt: "允许访问的目录绝对路径", Required: true}},
			Needs:  "需要 Node.js（npx）", Net: true,
		},
		{
			ID: "fetch", Name: "网页抓取",
			Desc:    "抓取网页并转成 Markdown，适合读文档",
			Command: "uvx", Args: []string{"mcp-server-fetch"},
			Needs: "需要 Python 与 uv（uvx）", Net: true,
		},
		{
			ID: "git", Name: "Git 仓库",
			Desc:    "查看提交历史、diff、分支，只读",
			Command: "uvx",
			Args:    []string{"mcp-server-git", "--repository", "{dir}"},
			Params:  []Param{{Key: "dir", Prompt: "git 仓库路径", Required: true}},
			Needs:   "需要 Python 与 uv（uvx）", Net: true,
		},
		{
			ID: "sqlite", Name: "SQLite 数据库",
			Desc:    "查询与浏览本地 .db 文件",
			Command: "uvx",
			Args:    []string{"mcp-server-sqlite", "--db-path", "{db}"},
			Params:  []Param{{Key: "db", Prompt: "数据库文件路径（.db）", Required: true}},
			Needs:   "需要 Python 与 uv（uvx）", Net: true,
		},
		{
			ID: "github", Name: "GitHub",
			Desc:    "读写仓库、Issue、PR",
			Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"},
			EnvKeys: []string{"GITHUB_PERSONAL_ACCESS_TOKEN"},
			Params: []Param{{
				Key: "token", Prompt: "GitHub Personal Access Token", Required: true,
				Env: true, Sensitive: true,
			}},
			Needs: "需要 Node.js（npx）与一个 GitHub Token", Net: true,
		},
		{
			ID: "memory", Name: "知识记忆",
			Desc:    "跨会话保存事实与偏好（知识图谱式）",
			Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-memory"},
			Needs: "需要 Node.js（npx）", Net: true,
		},
		{
			ID: "time", Name: "时区时间",
			Desc:    "查询任意时区的时间与转换",
			Command: "uvx", Args: []string{"mcp-server-time"},
			Needs: "需要 Python 与 uv（uvx）", Net: true,
		},
	}
}

// FindTemplate 按 ID 或序号（1 起）查找模板。
func FindTemplate(idOrIdx string) (Template, bool) {
	s := strings.TrimSpace(idOrIdx)
	if s == "" {
		return Template{}, false
	}
	if n, err := atoiSafe(s); err == nil && n >= 1 {
		list := Catalog()
		if n <= len(list) {
			return list[n-1], true
		}
		return Template{}, false
	}
	for _, t := range Catalog() {
		if strings.EqualFold(t.ID, s) {
			return t, true
		}
	}
	return Template{}, false
}

// Build 用用户填的值生成可直接落盘的 MCP 服务配置。
// 校验要求：必填项不能空；参数值里的引号要被规整，避免拼出坏命令。
func (t Template) Build(vals map[string]string) (core.MCPServer, error) {
	spec := core.MCPServer{Name: t.ID, Command: t.Command, Enabled: true}
	args := append([]string(nil), t.Args...)
	for _, p := range t.Params {
		v := strings.TrimSpace(vals[p.Key])
		if v == "" {
			if p.Required {
				return spec, fmt.Errorf("%s 需要填写：%s", t.Name, p.Prompt)
			}
			continue
		}
		v = strings.Trim(v, `"'`)
		if p.Env {
			// 敏感值只进环境变量：写进 args 会出现在进程列表与日志里。
			key := t.EnvKeys[0]
			spec.Env = append(spec.Env, key+"="+v)
			continue
		}
		if strings.ContainsAny(v, "\r\n") {
			return spec, fmt.Errorf("参数 %s 不应包含换行", p.Key)
		}
		placeholder := "{" + p.Key + "}"
		replaced := false
		for i, a := range args {
			if strings.Contains(a, placeholder) {
				args[i] = strings.ReplaceAll(a, placeholder, v)
				replaced = true
			}
		}
		if !replaced {
			args = append(args, v)
		}
	}
	spec.Args = args
	return spec, nil
}

// Validate 检查模板配置是否自洽（用于自检，避免模板写错却没人发现）。
func (t Template) Validate() error {
	if t.ID == "" || t.Name == "" || t.Command == "" {
		return fmt.Errorf("模板缺少 ID/名称/命令")
	}
	if strings.ContainsAny(t.ID, " \t\n") {
		return fmt.Errorf("模板 ID 不能含空格：%s", t.ID)
	}
	envParams := 0
	for _, p := range t.Params {
		if p.Key == "" || p.Prompt == "" {
			return fmt.Errorf("%s 的参数缺少 Key/Prompt", t.ID)
		}
		need := "{" + p.Key + "}"
		if p.Env {
			envParams++
			continue
		}
		found := false
		for _, a := range t.Args {
			if strings.Contains(a, need) {
				found = true
				break
			}
		}
		if !found && !p.Required {
			// 可选参数允许作为追加项
			continue
		}
		if !found {
			return fmt.Errorf("%s 的参数 %s 没有对应占位 %s", t.ID, p.Key, need)
		}
	}
	if envParams > len(t.EnvKeys) {
		return fmt.Errorf("%s 的环境变量参数多于 EnvKeys", t.ID)
	}
	return nil
}

// Installed 判断某模板是否已经装过（按 ID 比对，忽略大小写）。
func Installed(specs []core.MCPServer, id string) bool {
	for _, s := range specs {
		if strings.EqualFold(s.Name, id) {
			return true
		}
	}
	return false
}

// Names 返回已配置的服务名（按字母序），供 /mcp 列表展示。
func Names(specs []core.MCPServer) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}

// atoiSafe 解析非负整数。
func atoiSafe(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("空")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("非数字")
		}
		n = n*10 + int(r-'0')
		if n > 1e6 {
			return 0, fmt.Errorf("过大")
		}
	}
	return n, nil
}

// Probe 试连一个服务并返回工具数。它不写入全局注册表，
// 因此"安装失败"不会污染当前可用的工具集。
func Probe(spec core.MCPServer, timeoutSec int) (int, error) {
	c, err := Start(spec)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	list, err := c.ListTools()
	if err != nil {
		return 0, err
	}
	return len(list), nil
}
