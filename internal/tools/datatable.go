package tools

import (
	"fmt"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/coreutil"
)

func init() {
	register(Tool{
		Name: "datatable",
		Desc: "读取 CSV/TSV/JSON/JSONL 并做结构化查询：看表结构、切片、选列、按条件过滤、求和、分组计数。查数据时用它代替写脚本，避免把整张表塞进上下文。",
		Schema: `{
  "type":"object",
  "properties":{
    "path":{"type":"string","description":"数据文件路径（.csv/.tsv/.json/.jsonl）"},
    "op":{"type":"string","description":"info / head / select / filter / sum / group，默认 info"},
    "offset":{"type":"integer","description":"head 起始行，从 0 开始"},
    "limit":{"type":"integer","description":"返回行数，默认 20"},
    "columns":{"type":"string","description":"select 用，逗号分隔列名"},
    "column":{"type":"string","description":"filter/sum/group 的列名"},
    "operator":{"type":"string","description":"filter 比较符：= != > >= < <= contains startswith empty notempty"},
    "value":{"type":"string","description":"filter 比较值"},
    "top":{"type":"integer","description":"group 取前 N 组，默认 10"}
  },
  "required":["path","op"]
}`,
		Run: runDataTable,
	})
}

func runDataTable(ctx *Context, args map[string]interface{}) (Result, error) {
	p := core.ResolvePath(ctx.WorkDir, Get(args, "path"))
	if err := guardEscapes(ctx, p, "读取"); err != nil {
		return Result{}, err
	}
	t, err := coreutil.LoadTable(p)
	if err != nil {
		return Result{}, fmt.Errorf("读取表失败：%v", err)
	}
	op := strings.ToLower(strings.TrimSpace(Get(args, "op")))
	if op == "" {
		op = "info"
	}
	limit := GetInt(args, "limit", 20)
	if limit <= 0 {
		limit = 20
	}

	switch op {
	case "info":
		var b strings.Builder
		fmt.Fprintf(&b, "行数：%d，列数：%d\n列：%s\n", len(t.Rows), len(t.Columns), strings.Join(t.Columns, ", "))
		if t.Truncated {
			b.WriteString("（源文件超过 20 万行，已截断）\n")
		}
		b.WriteString("\n前 " + fmt.Sprint(minInt(limit, len(t.Rows))) + " 行预览：\n")
		b.WriteString(t.Slice(0, limit).Markdown(limit))
		return Result{Output: b.String(), Title: "表结构"}, nil

	case "head":
		sub := t.Slice(GetInt(args, "offset", 0), limit)
		return Result{Output: sub.Markdown(limit), Title: "数据预览"}, nil

	case "select":
		cols := splitList(Get(args, "columns"))
		if len(cols) == 0 {
			return Result{}, fmt.Errorf("columns 不能为空")
		}
		sub, err := t.Select(cols)
		if err != nil {
			return Result{}, err
		}
		return Result{Output: sub.Slice(0, limit).Markdown(limit), Title: "选列"}, nil

	case "filter":
		col := strings.TrimSpace(Get(args, "column"))
		sub, err := t.Filter(col, Get(args, "operator"), Get(args, "value"))
		if err != nil {
			return Result{}, err
		}
		out := fmt.Sprintf("命中 %d / %d 行\n\n", len(sub.Rows), len(t.Rows)) + sub.Slice(0, limit).Markdown(limit)
		return Result{Output: out, Title: "过滤结果"}, nil

	case "sum":
		col := strings.TrimSpace(Get(args, "column"))
		sum, n, err := t.Sum(col)
		if err != nil {
			return Result{}, err
		}
		return Result{Output: fmt.Sprintf("列 %s 求和：%g（参与 %d 行，跳过 %d 行非数值）\n", col, sum, n, len(t.Rows)-n)}, nil

	case "group":
		col := strings.TrimSpace(Get(args, "column"))
		top := GetInt(args, "top", 10)
		kvs, err := t.GroupCount(col, top)
		if err != nil {
			return Result{}, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "按 %s 分组（共 %d 组，展示前 %d）：\n", col, len(kvs), len(kvs))
		for _, kv := range kvs {
			k := kv.Key
			if k == "" {
				k = "(空)"
			}
			fmt.Fprintf(&b, "  %-30s %d\n", k, kv.Count)
		}
		return Result{Output: b.String(), Title: "分组统计"}, nil
	}
	return Result{}, fmt.Errorf("不支持的 op：%s（可用 info/head/select/filter/sum/group）", op)
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
