package tools

import (
	"fmt"
	"strings"

	"win7harness/internal/core"
)

// 项目长期记忆：.harness/memory.md
//
// 与上下文里的临时笔记不同，这里的条目会跨会话保留，并在每次启动时
// 以摘要形式注入系统提示。写入门槛必须高——记错一条，后面每个会话都会被它带偏。

func init() {
	register(Tool{
		Name: "memory_save",
		Desc: "把本次任务里确认过的、下次还会用到的事实写进项目长期记忆（.harness/memory.md）。" +
			"适合：项目的构建/测试命令、目录约定、外部服务地址、反复踩过的坑及其解法。" +
			"不适合：一次性的临时信息（本次改了哪个文件、某个会话的中间结论）、从代码里随时能读到的内容。",
		Schema: `{
  "type":"object",
  "properties":{
    "text":{"type":"string","description":"一条记忆，写成一句可独立理解的陈述，如：构建用 GOOS=windows GOARCH=386 ./build.sh，产物在 output/"},
    "tag":{"type":"string","description":"分类标签，如 build / test / env / gotcha / api，默认 note"}
  },
  "required":["text"]
}`,
		Run: runMemorySave,
	})
	register(Tool{
		Name: "memory_search",
		Desc: "在项目长期记忆里按关键词检索，返回命中的条目。动手前先查一下，避免重复踩坑或重复问用户。",
		Schema: `{
  "type":"object",
  "properties":{
    "query":{"type":"string","description":"关键词，多个用空格分隔；全部命中优先，未全中时按部分命中返回"},
    "limit":{"type":"integer","description":"最多返回条数，默认 10"}
  },
  "required":["query"]
}`,
		Run: runMemorySearch,
	})
	register(Tool{
		Name: "memory_list",
		Desc: "列出项目长期记忆的全部条目（按时间倒序）。用于人工核对记忆里到底写了什么。",
		Schema: `{
  "type":"object",
  "properties":{
    "limit":{"type":"integer","description":"最多返回条数，默认 50"}
  }
}`,
		Run: runMemoryList,
	})
}

func runMemorySave(ctx *Context, args map[string]interface{}) (Result, error) {
	text := strings.TrimSpace(Get(args, "text"))
	if text == "" {
		return Result{}, fmt.Errorf("text 不能为空")
	}
	if len([]rune(text)) > 400 {
		return Result{}, fmt.Errorf("单条记忆请控制在 400 字以内（当前 %d 字），过长的内容更适合写进 AGENTS.md", len([]rune(text)))
	}
	tag := strings.TrimSpace(Get(args, "tag"))
	if tag == "" {
		tag = "note"
	}
	path, err := core.AppendMemory(ctx.WorkDir, tag, text)
	if err != nil {
		return Result{}, err
	}
	// 去重提示：同一句话重复写入会让摘要被同类条目占满。
	dups := 0
	for _, e := range core.SearchMemory(ctx.WorkDir, strings.Fields(text), 20) {
		if strings.TrimSpace(e.Text) == strings.TrimSpace(text) {
			dups++
		}
	}
	out := "已写入项目记忆：" + text + "\n文件：" + path
	if dups > 1 {
		out += fmt.Sprintf("\n注意：记忆里已有 %d 条相同内容，后续可考虑合并或删除。", dups)
	}
	return Result{Output: out}, nil
}

func runMemorySearch(ctx *Context, args map[string]interface{}) (Result, error) {
	query := strings.TrimSpace(Get(args, "query"))
	if query == "" {
		return Result{}, fmt.Errorf("query 不能为空")
	}
	limit := GetInt(args, "limit", 10)
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	hits := core.SearchMemory(ctx.WorkDir, strings.Fields(query), limit)
	if len(hits) == 0 {
		return Result{Output: "项目记忆里没有匹配「" + query + "」的条目。相关事实可考虑用 memory_save 记下来。"}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "命中 %d 条项目记忆：\n", len(hits))
	for _, e := range hits {
		fmt.Fprintf(&b, "- [%s] %s", e.Tag, e.Text)
		if e.At != "" {
			fmt.Fprintf(&b, "  （%s）", e.At)
		}
		b.WriteString("\n")
	}
	return Result{Output: strings.TrimRight(b.String(), "\n")}, nil
}

func runMemoryList(ctx *Context, args map[string]interface{}) (Result, error) {
	limit := GetInt(args, "limit", 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	all := core.LoadMemory(ctx.WorkDir)
	if len(all) == 0 {
		return Result{Output: "项目还没有任何长期记忆（.harness/memory.md 不存在或为空）。"}, nil
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "共 %d 条记忆（显示最近 %d 条）：\n", len(core.LoadMemory(ctx.WorkDir)), len(all))
	for i := len(all) - 1; i >= 0; i-- {
		e := all[i]
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, e.Tag, e.Text)
	}
	return Result{Output: strings.TrimRight(b.String(), "\n")}, nil
}
