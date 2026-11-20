package agent

import (
	"encoding/json"
	"strings"
)

// parseArgs 容错解析模型给出的 JSON 参数（部分模型会包一层 ``` 或单引号）。
func parseArgs(raw string) (map[string]interface{}, error) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "{}" {
		return map[string]interface{}{}, nil
	}
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err == nil {
		return m, nil
	}
	// 截断或多包裹的 JSON：往往只差一个括号或后面多了句解释。
	if fixed, ok := repairJSON(s); ok {
		if err := json.Unmarshal([]byte(fixed), &m); err == nil {
			return m, nil
		}
		if relaxed, rok := relaxJSON(fixed); rok {
			if err := json.Unmarshal([]byte(relaxed), &m); err == nil {
				return m, nil
			}
		}
	}
	// 退化：数字键/单引号等非法 JSON，尝试按宽松方式解析为字符串参数
	if fixed, ok := relaxJSON(s); ok {
		if err := json.Unmarshal([]byte(fixed), &m); err == nil {
			return m, nil
		}
	}
	// 兜底：模型吐出的裸文本只填 command（限定 run_command 语义）。
	// 此前同时填 path/command/content，整段文本会被当成文件路径/内容：
	// 对 write_file 就是“把整段话写进一个以它命名的文件”——宁可报错让模型重来。
	if !strings.HasPrefix(s, "{") {
		return map[string]interface{}{"command": s}, nil
	}
	return nil, jsonErr(s)
}

func jsonErr(s string) error {
	var m map[string]interface{}
	err := json.Unmarshal([]byte(s), &m)
	if err != nil {
		return err
	}
	return nil
}

// relaxJSON 修复常见模型输出瑕疵：尾逗号、单引号、未转义换行。
// 单引号替换只在“整串不含双引号”时进行——含双引号说明原文本是双引号 JSON，
// 里面出现的单引号多半是内容（如文中 `'` 字符），盲替换会静默改写合法内容。
func relaxJSON(s string) (string, bool) {
	orig := s
	if !strings.Contains(s, `"`) {
		s = strings.ReplaceAll(s, "'", "\"")
	}
	// 去掉对象/数组尾逗号
	for _, pair := range [][2]string{
		{",}", "}"},
		{", ]", "]"},
		{",]", "]"},
		{",\n}", "\n}"},
		{",\r\n}", "\r\n}"},
	} {
		s = strings.ReplaceAll(s, pair[0], pair[1])
	}
	// 修复 JSON 字符串内的裸换行
	out := make([]byte, 0, len(s))
	inStr := false
	esc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				out = append(out, c)
				esc = false
				continue
			}
			switch c {
			case '\\':
				out = append(out, c)
				esc = true
			case '"':
				out = append(out, c)
				inStr = false
			case '\n':
				out = append(out, '\\', 'n')
			case '\r':
			case '\t':
				out = append(out, '\\', 't')
			default:
				out = append(out, c)
			}
			continue
		}
		if c == '"' {
			inStr = true
		}
		out = append(out, c)
	}
	return string(out), string(out) != orig
}

// repairJSON 处理模型输出 JSON 的另外两类高频故障：
//  1. 内容被 max_tokens 截断，对象/数组没闭合；
//  2. JSON 后面跟了一句解释（"以上是参数，请执行"），或前面加了客套话。
//
// 做法是按字符串状态机重扫一遍，剥离首尾杂质，然后按未闭合的栈补齐括号。
func repairJSON(s string) (string, bool) {
	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return s, false
	}
	body := s[start:]

	var stack []byte
	inStr, esc := false, false
	end := -1
	for i := 0; i < len(body); i++ {
		c := body[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			stack = append(stack, c)
		case '}', ']':
			if len(stack) == 0 {
				end = i // 多余的右括号：说明 JSON 到这里已结束
				break
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end >= 0 {
		out := strings.TrimSpace(body[:end+1])
		return out, out != s
	}

	// 没闭合：先收口字符串，再补右括号（顺序必须与开栈相反）。
	var b strings.Builder
	b.WriteString(body)
	if inStr {
		if esc {
			b.WriteByte('\\')
		}
		b.WriteByte('"')
	}
	trimmed := strings.TrimRight(b.String(), " \t\r\n")
	trimmed = strings.TrimRight(trimmed, ",")
	b.Reset()
	b.WriteString(trimmed)
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			b.WriteByte('}')
		} else {
			b.WriteByte(']')
		}
	}
	return b.String(), true
}

// RepairToolArgs 是 parseArgs 的对外版本：把模型吐出的（可能带杂质、可能被截断的）
// 工具参数 JSON 尽量修好再解析。自检与调试用得上。
func RepairToolArgs(raw string) (map[string]interface{}, error) {
	return parseArgs(raw)
}
