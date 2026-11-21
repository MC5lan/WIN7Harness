package tools

import (
	"fmt"
	"sort"
	"strings"
)

// 工具目录的“按需加载”入口：小窗口模型默认只带 core 工具，
// 需要联网/表格/诊断等能力时先 tool_search 查名字，再 tool_load 加入下一轮。
// 这样既不把 30+ 个 schema 全塞进 8k 模型，也不会让用户觉得“工具消失了”。

func init() {
	register(Tool{
		Name: "tool_search",
		Desc: "搜索当前可用的扩展工具（core profile 下未默认发送的工具）。返回工具名、分类与用途；" +
			"找到后调用 tool_load 加载，下一轮才会真正出现在工具列表里。",
		Schema: `{
  "type":"object",
  "properties":{
    "query":{"type":"string","description":"按工具名/用途/分类过滤，例如 web、测试、表格；留空列出全部扩展工具"}
  }
}`,
		Run: runToolSearch,
	})
	register(Tool{
		Name: "tool_load",
		Desc: "加载一个扩展工具，使其从下一轮开始可调用。只加载当前任务真正需要的工具；" +
			"计划模式或子代理白名单之外的工具会被拒绝。",
		Schema: `{
  "type":"object",
  "properties":{
    "name":{"type":"string","description":"工具名，来自 tool_search"}
  },
  "required":["name"]
}`,
		Run: runToolLoad,
	})
}

func runToolSearch(ctx *Context, args map[string]interface{}) (Result, error) {
	q := strings.ToLower(strings.TrimSpace(Get(args, "query")))
	var lines []string
	for _, t := range All() {
		if t.Hidden || t.Core {
			continue
		}
		hay := strings.ToLower(t.Name + " " + t.Category + " " + t.Desc)
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s（%s）：%s", t.Name, t.Category, firstToolLine(t.Desc)))
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return Result{Output: "没有匹配的扩展工具。core 工具已经默认可用；换个关键词再试。"}, nil
	}
	if len(lines) > 30 {
		lines = append(lines[:30], "…（结果过多，请缩小关键词）")
	}
	return Result{
		Output:  "可用扩展工具：\n" + strings.Join(lines, "\n") + "\n\n需要哪个就调用 tool_load，下一轮会加入工具列表。",
		Display: true,
		Title:   "扩展工具",
	}, nil
}

func runToolLoad(ctx *Context, args map[string]interface{}) (Result, error) {
	name := strings.TrimSpace(Get(args, "name"))
	if name == "" {
		return Result{}, fmt.Errorf("name 不能为空，先用 tool_search 查找")
	}
	t, ok := Find(name)
	if !ok || t.Hidden {
		return Result{}, fmt.Errorf("不存在工具 %q；先用 tool_search 查找", name)
	}
	if t.Core {
		return Result{Output: "工具 " + name + " 已默认可用，无需加载。"}, nil
	}
	if ctx != nil && ctx.LoadTool == nil {
		return Result{}, fmt.Errorf("当前环境不支持按需加载工具")
	}
	if ctx != nil && !ctx.LoadTool(name) {
		return Result{}, fmt.Errorf("当前模式或白名单不允许加载工具 %s", name)
	}
	return Result{Output: "已加载工具 " + name + "，下一轮开始可调用。"}, nil
}

func firstToolLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n。"); i > 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > 100 {
		return string(r[:100]) + "…"
	}
	return s
}
