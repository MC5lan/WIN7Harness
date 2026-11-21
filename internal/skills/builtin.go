package skills

import (
	"embed"
	"os"
	"sort"
	"strings"
	"sync"
)

// 内置技能随二进制分发：项目/全局技能优先，内置技能优先级最低。
// 不往用户目录写副本，升级后不会出现“旧副本覆盖新版本”的问题；
// 用户需要改时，在技能页点“复制到项目/全局”再编辑。
//
//go:embed builtin/*.md
var builtinFS embed.FS

// builtinRevision 参与 ScanMeta 缓存键；改内置技能内容时同步 +1。
const builtinRevision = "1"

var (
	builtinOnce sync.Once
	builtinList []Skill
)

// BuiltinSkills 返回内置技能（只读，按名称排序）。
func BuiltinSkills() []Skill {
	// 测试与排障可临时关闭内置技能；正常用户通过配置里的 disabled_skills 管理。
	if os.Getenv("WIN7H_DISABLE_BUILTIN_SKILLS") == "1" {
		return nil
	}
	builtinOnce.Do(func() {
		entries, err := builtinFS.ReadDir("builtin")
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
				continue
			}
			data, err := builtinFS.ReadFile("builtin/" + e.Name())
			if err != nil {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".md")
			sk := parseDoc(name, string(data), "embedded:"+e.Name(), "builtin", "embedded")
			sk.ReadOnly = true
			sk.HasDoc = true
			if sk.Name == "" {
				continue
			}
			builtinList = append(builtinList, sk)
		}
		sort.SliceStable(builtinList, func(i, j int) bool {
			return strings.ToLower(builtinList[i].Name) < strings.ToLower(builtinList[j].Name)
		})
	})
	out := make([]Skill, len(builtinList))
	copy(out, builtinList)
	return out
}

// BuiltinCount 返回内置技能数量，供自检/UI 使用。
func BuiltinCount() int { return len(BuiltinSkills()) }
