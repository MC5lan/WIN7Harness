package tools

import (
	"fmt"
	"strings"

	"win7harness/internal/codeidx"
)

func init() {
	register(Tool{
		Name: "code_search",
		Desc: "在工作区代码索引里按符号名查找函数、方法、类型、类（Go 用语法树解析，其他语言用轻量规则），返回 文件:行号 与签名。适合“这个函数在哪定义/这个类型有哪些方法”。传 file 可只看某个文件的符号大纲。索引是本地的、增量的，不需要联网或外部工具。",
		Schema: `{
  "type":"object",
  "properties":{
    "query":{"type":"string","description":"符号名或其片段，支持驼峰/下划线部分匹配"},
    "file":{"type":"string","description":"可选，只在这个文件里列符号（大纲模式）"},
    "limit":{"type":"integer","description":"返回条数上限，默认 20，最大 100"},
    "refresh":{"type":"boolean","description":"是否强制重建索引，默认 false（增量为准）"}
  }
}`,
		Run: runCodeSearch,
	})
}

func runCodeSearch(ctx *Context, args map[string]interface{}) (Result, error) {
	query := strings.TrimSpace(Get(args, "query"))
	file := strings.TrimSpace(Get(args, "file"))
	if query == "" && file == "" {
		return Result{}, fmt.Errorf("query 与 file 至少给一个")
	}
	limit := 20
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}
	refresh := false
	if v, ok := args["refresh"].(bool); ok {
		refresh = v
	}

	root := ctx.WorkDir
	if root == "" {
		return Result{}, fmt.Errorf("工作目录为空，无法建立代码索引")
	}

	var ix *codeidx.Index
	switch {
	case refresh:
		ix = codeidx.Refresh(root)
	case codeidx.Cached() != nil && codeidx.Cached().Root == root:
		ix = codeidx.Cached()
	default:
		ix = codeidx.Build(root, codeidx.Cached())
	}

	files, syms := ix.Stats()
	head := fmt.Sprintf("索引：%d 个文件 / %d 个符号", files, syms)
	if ix.Truncated {
		head += "（已截断，仓库过大时只索引前若干文件）"
	}

	var hits []codeidx.Symbol
	if file != "" {
		f := strings.ReplaceAll(file, "\\", "/")
		f = strings.TrimPrefix(f, "./")
		for _, s := range ix.Syms {
			if strings.EqualFold(s.File, f) || strings.HasSuffix(strings.ToLower(s.File), "/"+strings.ToLower(f)) {
				hits = append(hits, s)
			}
		}
		if len(hits) == 0 {
			return Result{Output: head + "\n\n索引里没有这个文件：" + file + "\n提示：路径用相对工作区的写法，例如 internal/app/pages.go"}, nil
		}
		if query != "" {
			low := strings.ToLower(query)
			kept := hits[:0]
			for _, s := range hits {
				if strings.Contains(strings.ToLower(s.Name), low) {
					kept = append(kept, s)
				}
			}
			hits = kept
		}
		if limit > 0 && len(hits) > limit {
			hits = hits[:limit]
		}
		var b strings.Builder
		b.WriteString(head + "\n\n")
		for _, s := range hits {
			fmt.Fprintf(&b, "%s:%d  [%s] %s\n", s.File, s.Line, s.Kind, s.Sig)
		}
		return Result{Output: strings.TrimRight(b.String(), "\n")}, nil
	}

	hits = ix.Search(query, limit)
	if len(hits) == 0 {
		msg := head + "\n\n没有匹配 “" + query + "” 的符号。"
		msg += "\n建议：换更短的关键词（如 parse 而不是 parseEverything）；或确认目标代码在工作区内（已跳过 vendor、node_modules、dist 等目录）。"
		return Result{Output: msg}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n匹配 “%s” 的符号：\n\n", head, query)
	for _, s := range hits {
		fmt.Fprintf(&b, "%s:%d  [%s] %s\n", s.File, s.Line, s.Kind, s.Sig)
	}
	return Result{Output: strings.TrimRight(b.String(), "\n")}, nil
}
