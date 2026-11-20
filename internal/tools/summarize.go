package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// 工具输出智能摘要。
//
// 问题很具体：一条 grep 或 go test 的输出动辄几万行，原样塞回模型上下文既贵又没用，
// 而简单粗暴地"砍掉后面"会把唯一的错误行也砍掉——模型只能瞎猜。
// 这里的策略是：先保留头尾（头有命令与概要，尾有最终结论），再从被省略的中间
// 段里把"看起来重要"的行挑出来（错误、警告、失败、文件路径、行号），
// 最后在预算内拼回去。这样既压得住体积，又不会丢关键线索。

const (
	// DefaultToolOutputBudget 单次工具输出回灌上下文的字符预算。
	// 取 12000 字符（约 4k token）：足够放下一次完整测试失败摘要，
	// 又不会让一次 grep 把整个上下文吃满。
	DefaultToolOutputBudget = 12000

	// summaryHeadLines / summaryTailLines 是头尾保留的行数。
	summaryHeadLines = 20
	summaryTailLines = 30

	// summaryKeyMax 是被省略段里最多挑出的关键行数，防止全是报错时又膨胀回去。
	summaryKeyMax = 120
)

// importantMarkers 是判定"这一行值得保留"的标记。
// 全部用小写做包含匹配，避免大小写差异漏判。
var importantMarkers = []string{
	"error", "错误", "fatal", "panic", "exception", "traceback",
	"fail", "失败", "cannot", "无法", "not found", "找不到", "未找到",
	"denied", "拒绝", "warning", "警告", "undefined", "null pointer",
	"build failed", "编译失败", "syntax error", "out of memory", "timeout", "超时",
}

// locPattern 匹配 "路径:行号" 形式的定位信息，如 foo/bar.go:123、
// C:\x\y.py:12:5、./a/b.rs:9。要求带路径分隔符，避免把 "12:30" 这类时间当成定位。
var locPattern = regexp.MustCompile(`[^\s:]*[/\\][^\s:]*:\d+`)

// lineLooksImportant 判断一行是否应被保留。
// 除关键字外，还认"文件:行号"这种定位信息——它通常是修复的唯一入口。
func lineLooksImportant(line string) bool {
	l := strings.ToLower(line)
	for _, m := range importantMarkers {
		if strings.Contains(l, m) {
			return true
		}
	}
	return locPattern.MatchString(line)
}

// SummarizeToolOutput 把过长的工具输出压成"头 + 关键行 + 尾"，未超预算时原样返回。
// budget <= 0 时用 DefaultToolOutputBudget。
func SummarizeToolOutput(out string, budget int) string {
	if budget <= 0 {
		budget = DefaultToolOutputBudget
	}
	if len(out) <= budget {
		return out
	}
	// 行数很少但单行极长（压缩后的 JSON、base64）时按字符窗口截断，
	// 硬按行处理会切碎一个逻辑单元。
	lines := strings.Split(out, "\n")
	if len(lines) < summaryHeadLines+summaryTailLines+5 {
		return cutMiddle(out, budget)
	}

	head := lines[:summaryHeadLines]
	tail := lines[len(lines)-summaryTailLines:]

	var keys []string
	keySeen := map[string]bool{}
	omitted := 0
	extraBytes := 0
	for _, ln := range lines[summaryHeadLines : len(lines)-summaryTailLines] {
		if lineLooksImportant(ln) {
			// 同一条错误重复几百遍时只留一份，后面标次数
			t := strings.TrimSpace(ln)
			if keySeen[t] {
				continue
			}
			keySeen[t] = true
			if len(keys) < summaryKeyMax {
				keys = append(keys, ln)
				extraBytes += len(ln) + 1
				continue
			}
		}
		omitted++
	}

	var b strings.Builder
	b.Grow(budget + 1024)
	for _, ln := range head {
		b.WriteString(ln)
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("\n...（中间 %d 行已省略，全文 %d 行 / %s；下面只列关键行）...\n",
		omitted, len(lines), humanChars(len(out))))
	if len(keys) > 0 {
		for _, ln := range keys {
			if b.Len() > budget {
				break
			}
			b.WriteString(ln)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("（中间段落没有识别到错误、警告或文件定位行）\n")
	}
	b.WriteString(fmt.Sprintf("...（续末尾 %d 行）...\n", len(tail)))
	for _, ln := range tail {
		if b.Len() > budget+4096 {
			break
		}
		b.WriteString(ln)
		b.WriteString("\n")
	}
	res := b.String()
	// 头尾本身就超预算（极端长行）时兜底硬截断，保证不会二次膨胀。
	if len(res) > budget+4096 {
		res = cutMiddle(res, budget)
	}
	return res
}

// cutMiddle 保留首尾各一段，中间用省略标记，用于超长单行内容。
// 按 rune 截取：按字节截会把汉字从中间劈开，产生非法 UTF-8（UI 里是乱码）。
func cutMiddle(s string, budget int) string {
	r := []rune(s)
	if len(r) <= budget {
		return s
	}
	keep := budget - 120
	if keep < budget/2 {
		keep = budget / 2
	}
	head := keep * 2 / 3
	tail := keep - head
	return string(r[:head]) + fmt.Sprintf("\n...（中间 %s 已省略）...\n", humanChars(len(r)-keep)) + string(r[len(r)-tail:])
}

// humanChars 把字符数格式化成人读的大小。
func humanChars(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d 字符", n)
	}
}

// OutputWasTrimmed 判断输出是否已被摘要处理，供界面打标（用户能看到"已压缩"）。
func OutputWasTrimmed(origin, shown string) bool {
	return len(shown) < len(origin) && strings.Contains(shown, "已省略")
}
