// Package commands 发现并加载用户自定义的斜杠命令（Markdown 即配置）。
//
// 目录约定（与 skills 包一致，项目级覆盖全局同名命令）：
//
//	<工作目录>/.harness/commands/<name>.md
//	<DataDir>/commands/<name>.md
//
// 文件格式：
//
//	---
//	name: hello
//	description: 打个招呼
//	---
//	请向用户问好。用户说：{{args}}
package commands

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/skills"
)

// Command 一条自定义命令。
type Command struct {
	Name  string
	Desc  string
	Body  string
	Path  string
	Scope string // "project" | "global"
}

// ProjectDir 返回工作区级命令目录。
func ProjectDir(workDir string) string {
	return filepath.Join(workDir, ".harness", "commands")
}

// GlobalDir 返回全局命令目录。
func GlobalDir() string {
	return filepath.Join(core.DataDir(), "commands")
}

// Scan 扫描两个作用域的命令，项目级优先（同名时覆盖全局）。
func Scan(workDir string) []Command {
	var out []Command
	seen := map[string]bool{}
	dirs := []struct {
		dir, scope string
	}{{ProjectDir(workDir), "project"}, {GlobalDir(), "global"}}
	for _, d := range dirs {
		entries, err := os.ReadDir(d.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
				continue
			}
			c := loadFile(filepath.Join(d.dir, e.Name()), d.scope)
			if c.Name == "" || seen[strings.ToLower(c.Name)] {
				continue
			}
			seen[strings.ToLower(c.Name)] = true
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Find 按名字查找命令（大小写不敏感）。
func Find(workDir, name string) (Command, bool) {
	name = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/"))
	for _, c := range Scan(workDir) {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return Command{}, false
}

// Expand 把 {{args}} / $ARGUMENTS 替换为用户跟在命令后面的参数。
func (c Command) Expand(args string) string {
	body := strings.ReplaceAll(c.Body, "{{args}}", args)
	body = strings.ReplaceAll(body, "$ARGUMENTS", args)
	return strings.TrimSpace(body)
}

func loadFile(path, scope string) Command {
	data, err := os.ReadFile(path)
	if err != nil {
		return Command{}
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	meta, body := skills.SplitFrontMatter(text)
	name := strings.TrimSpace(meta["name"])
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	// 命令名必须是合法的 /token：只留字母数字、下划线和中划线。
	name = sanitize(name)
	desc := strings.TrimSpace(meta["description"])
	if desc == "" {
		desc = strings.TrimSpace(meta["desc"])
	}
	if desc == "" {
		for _, ln := range strings.Split(body, "\n") {
			if s := strings.TrimSpace(ln); s != "" && !strings.HasPrefix(s, "#") {
				desc = s
				break
			}
		}
	}
	if len([]rune(desc)) > 80 {
		desc = string([]rune(desc)[:80]) + "…"
	}
	if len([]rune(body)) > 20000 {
		body = string([]rune(body)[:20000])
	}
	return Command{Name: name, Desc: desc, Body: body, Path: path, Scope: scope}
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r > 127 {
			b.WriteRune(r) // 允许中文命令名
		} else {
			break
		}
	}
	return strings.Trim(b.String(), "-_")
}
