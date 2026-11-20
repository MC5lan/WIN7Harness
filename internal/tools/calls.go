package tools

import (
	"fmt"
	"strings"

	"win7harness/internal/codeidx"
)

func init() {
	register(Tool{
		Name: "calls",
		Desc: "检索函数的调用关系：谁调用了它（callers）、它调用了谁（callees）。改函数签名、评估改动影响面、定位入口时优先用它，比 grep 准确。",
		Schema: `{
  "type":"object",
  "properties":{
    "symbol":{"type":"string","description":"函数名，支持 Fn、Type.Method、pkg.Fn"},
    "direction":{"type":"string","description":"callers（谁调用它）/ callees（它调用谁）/ both，默认 both"},
    "limit":{"type":"integer","description":"每侧最多返回条数，默认 30"}
  },
  "required":["symbol"]
}`,
		Run: runCalls,
	})
}

func runCalls(ctx *Context, args map[string]interface{}) (Result, error) {
	sym := strings.TrimSpace(Get(args, "symbol"))
	if sym == "" {
		return Result{}, fmt.Errorf("symbol 不能为空")
	}
	dir := strings.TrimSpace(Get(args, "direction"))
	if dir == "" {
		dir = "both"
	}
	limit := GetInt(args, "limit", 30)

	g := codeidx.Graph(ctx.WorkDir)
	if g.Funcs == 0 {
		return Result{}, fmt.Errorf("没解析到 Go 函数（目录 %s 可能不是 Go 项目）", ctx.WorkDir)
	}
	name := g.Resolve(sym)
	var b strings.Builder
	if amb := g.Ambiguous(sym); len(amb) > 0 {
		fmt.Fprintf(&b, "注意：%s 有 %d 个同名候选：%s（默认展示 %s）\n\n", sym, len(amb), strings.Join(amb, ", "), name)
	}

	if site, ok := g.DeclSite(name); ok {
		fmt.Fprintf(&b, "定义：%s:%d\n", site.File, site.Line)
	} else {
		fmt.Fprintf(&b, "未找到 %s 的定义（按名字匹配继续查询调用点）\n", name)
	}

	if dir == "callers" || dir == "both" {
		cs := g.Callers(name, limit)
		fmt.Fprintf(&b, "\n调用方（%d 处）：\n", len(cs))
		if len(cs) == 0 {
			b.WriteString("  （无，可能是入口函数或未被引用）\n")
		}
		for _, c := range cs {
			fmt.Fprintf(&b, "  %s:%d  ← %s\n", c.File, c.Line, c.From)
		}
	}
	if dir == "callees" || dir == "both" {
		cl := g.Callees(name)
		fmt.Fprintf(&b, "\n直接调用的函数（%d 个）：\n", len(cl))
		if len(cl) == 0 {
			b.WriteString("  （无）\n")
		}
		for i, c := range cl {
			if i >= limit {
				fmt.Fprintf(&b, "  …还有 %d 个\n", len(cl)-limit)
				break
			}
			b.WriteString("  → " + c + "\n")
		}
	}
	return Result{Output: b.String(), Title: "调用关系 " + name}, nil
}
