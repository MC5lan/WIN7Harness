package core

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 批处理任务清单解析。
//
// 一行一个任务，`#` 开头是注释，空行跳过。支持 {{date}} / {{time}} / {{datetime}} /
// {{dir}} 四个占位符——"每天凌晨整理某目录"这类定时任务靠这个一行复用到不同日期。
//
// 编码：Win7 记事本默认存 ANSI，另存对话框里会给出 ANSI / UTF-8 / Unicode 三种。
// 这里能认 UTF-8（含 BOM）和 UTF-16 LE/BE（带 BOM），其余按 UTF-8 读并在解析时明确报错，
// 不让用户拿到一堆乱码任务去执行。

// ScriptTask 清单里的一条任务。
type ScriptTask struct {
	No   int // 序号，从 1 开始
	Line int // 源文件行号，报错时定位用
	Text string
}

// DecodeScript 把清单文件的原始字节转成文本。
func DecodeScript(raw []byte) (string, error) {
	if len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE { // UTF-16 LE
		return decodeUTF16(raw[2:], binary.LittleEndian)
	}
	if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF { // UTF-16 BE
		return decodeUTF16(raw[2:], binary.BigEndian)
	}
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	if !utf8.Valid(raw) {
		return "", fmt.Errorf("任务清单不是 UTF-8 编码（可能是 ANSI/GBK），请用记事本另存为 UTF-8")
	}
	return string(raw), nil
}

func decodeUTF16(b []byte, order binary.ByteOrder) (string, error) {
	if len(b)%2 != 0 {
		return "", fmt.Errorf("UTF-16 任务清单长度异常（%d 字节），文件可能已损坏", len(b))
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, order.Uint16(b[i:i+2]))
	}
	runes := make([]rune, 0, len(u))
	for i := 0; i < len(u); i++ {
		c := u[i]
		if c >= 0xD800 && c <= 0xDBFF && i+1 < len(u) {
			low := u[i+1]
			if low >= 0xDC00 && low <= 0xDFFF {
				runes = append(runes, 0x10000+((rune(c)-0xD800)<<10)+(rune(low)-0xDC00))
				i++
				continue
			}
		}
		runes = append(runes, rune(c))
	}
	return string(runes), nil
}

// ScriptVars 生成占位符取值表。now 由调用方传入，便于测试。
func ScriptVars(dir string, date, clock string) map[string]string {
	return map[string]string{
		"date":     date,
		"time":     clock,
		"datetime": date + " " + clock,
		"dir":      dir,
	}
}

// ExpandVars 替换 {{key}} 占位符，未知的保持原样（方便用户写 {{path}} 之类自己再填）。
func ExpandVars(s string, vars map[string]string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	out := s
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

// ParseScript 解析任务清单。vars 为 nil 时不做占位符替换。
func ParseScript(text string, vars map[string]string) ([]ScriptTask, error) {
	var tasks []ScriptTask
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if vars != nil {
			line = ExpandVars(line, vars)
		}
		tasks = append(tasks, ScriptTask{No: len(tasks) + 1, Line: i + 1, Text: line})
	}
	return tasks, nil
}

// TaskStatus 供脚本批处理汇总用。
type TaskStatus struct {
	OK       bool
	TimedOut bool
}

// SummarizeScript 汇总批处理结果并给出进程退出码。
// 退出码约定与单任务保持一致：0 全成功、1 有失败或未跑完、3 超时。
// 抽出来单独测：批处理脚本普遍靠退出码判断成败，这里错一次会连累所有自动化。
func SummarizeScript(total int, results []TaskStatus) (ok, fail, skipped, code int) {
	for _, r := range results {
		if r.OK {
			ok++
		} else {
			fail++
		}
	}
	if total > len(results) {
		skipped = total - len(results)
	}
	switch {
	case hasTimeout(results):
		code = 3
	case fail > 0 || skipped > 0:
		code = 1
	default:
		code = 0
	}
	return
}

func hasTimeout(results []TaskStatus) bool {
	for _, r := range results {
		if r.TimedOut {
			return true
		}
	}
	return false
}
