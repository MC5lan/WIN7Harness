package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 自定义子代理：放在 <工作区>/.harness/agents/*.md，随项目走、可进版本库。
//
// 文件格式（YAML 风格的前置元信息 + 正文）：
//
//	---
//	name: reviewer
//	description: 只读代码审查，输出问题清单
//	tools: read_file, code_search, run_command
//	model: fast
//	rounds: 8
//	---
//	你是一名严格的代码审查者……
//
// 正文会追加到子代理的系统提示末尾；没写 tools 表示继承父级全部工具。
// 解析刻意保持宽松：前置块写错不影响正文生效，只是一条提示（便于用户排查）。

// AgentDef 是一份自定义子代理定义。
type AgentDef struct {
	Name        string
	Description string
	Tools       []string
	Model       string // main | fast（留空继承父级）
	MaxRounds   int    // 0 表示用默认值
	Body        string
	Path        string
}

// AgentsDir 返回工作区下的子代理定义目录。
func AgentsDir(workDir string) string {
	if strings.TrimSpace(workDir) == "" {
		return ""
	}
	return filepath.Join(workDir, ".harness", "agents")
}

// LoadAgentDefs 读取目录下全部定义。warn 汇总解析问题，不阻断使用。
func LoadAgentDefs(workDir string) (defs []AgentDef, warn string) {
	dir := AgentsDir(workDir)
	if dir == "" {
		return nil, ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, ""
	}
	var warns []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(full)
		if err != nil {
			warns = append(warns, e.Name()+"：读取失败")
			continue
		}
		def, w := parseAgentDef(string(raw), full)
		if w != "" {
			warns = append(warns, e.Name()+"："+w)
		}
		defs = append(defs, def)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs, strings.Join(warns, "；")
}

func parseAgentDef(raw, path string) (AgentDef, string) {
	def := AgentDef{Path: path}
	body := raw
	var warn string
	if strings.HasPrefix(strings.TrimSpace(raw), "---") {
		trimmed := strings.TrimSpace(raw)
		rest := strings.TrimPrefix(trimmed, "---")
		if i := strings.Index(rest, "\n---"); i >= 0 {
			head := rest[:i]
			body = strings.TrimSpace(rest[i+4:])
			for _, line := range strings.Split(head, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				k, v, ok := strings.Cut(line, ":")
				if !ok {
					warn = "无法识别的行：" + line
					continue
				}
				k = strings.ToLower(strings.TrimSpace(k))
				v = strings.Trim(strings.TrimSpace(v), `"'`)
				switch k {
				case "name":
					def.Name = v
				case "description", "desc":
					def.Description = v
				case "tools":
					def.Tools = splitList(v)
				case "model":
					def.Model = strings.ToLower(v)
				case "rounds", "max_rounds":
					fmt.Sscanf(v, "%d", &def.MaxRounds)
				}
			}
		} else {
			warn = "缺少结束的 --- 分隔行，正文按整篇处理"
		}
	}
	def.Body = strings.TrimSpace(body)
	if def.Name == "" {
		def.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if def.Description == "" {
		// 用正文首行兜底，保证选择列表里不是一串空白。
		def.Description = firstLineOf(def.Body, 60)
	}
	return def, warn
}

func splitList(v string) []string {
	v = strings.ReplaceAll(v, "，", ",")
	parts := strings.Split(v, ",")
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(strings.Trim(p, `"'[]`)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstLineOf(s string, n int) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		r := []rune(line)
		if len(r) > n {
			return string(r[:n]) + "…"
		}
		return line
	}
	return ""
}

// FindAgent 按名字查定义（大小写不敏感）。
func FindAgent(defs []AgentDef, name string) (AgentDef, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, d := range defs {
		if strings.ToLower(d.Name) == name {
			return d, true
		}
	}
	return AgentDef{}, false
}

// Allows 判断该定义是否允许某工具；未限定工具时一律允许。
func (d AgentDef) Allows(tool string) bool {
	if len(d.Tools) == 0 {
		return true
	}
	for _, t := range d.Tools {
		if t == tool || t == "*" {
			return true
		}
	}
	return false
}

// Describe 给 /agents 列表用的单行描述。
func (d AgentDef) Describe() string {
	var b strings.Builder
	b.WriteString(d.Name)
	if d.Description != "" {
		b.WriteString(" — " + d.Description)
	}
	var meta []string
	if len(d.Tools) > 0 {
		meta = append(meta, "工具："+strings.Join(d.Tools, ","))
	} else {
		meta = append(meta, "工具：全部继承")
	}
	if d.Model != "" {
		meta = append(meta, "模型："+d.Model)
	}
	if d.MaxRounds > 0 {
		meta = append(meta, fmt.Sprintf("轮数：%d", d.MaxRounds))
	}
	if len(meta) > 0 {
		b.WriteString("\n    " + strings.Join(meta, " · "))
	}
	return b.String()
}

var agentsCache struct {
	sync.Mutex
	dir   string
	mtime time.Time
	defs  []AgentDef
}

// LoadAgentDefsCached 带目录 mtime 缓存的版本：工具调用频率高，
// 不能每次执行工具都重新读一遍 .harness/agents。
func LoadAgentDefsCached(workDir string) []AgentDef {
	dir := AgentsDir(workDir)
	if dir == "" {
		return nil
	}
	var mtime time.Time
	if st, err := os.Stat(dir); err == nil {
		mtime = st.ModTime()
	} else {
		return nil
	}
	agentsCache.Lock()
	if agentsCache.dir == dir && agentsCache.mtime.Equal(mtime) {
		defs := agentsCache.defs
		agentsCache.Unlock()
		return defs
	}
	agentsCache.Unlock()

	defs, _ := LoadAgentDefs(workDir)

	agentsCache.Lock()
	agentsCache.dir, agentsCache.mtime, agentsCache.defs = dir, mtime, defs
	agentsCache.Unlock()
	return defs
}
