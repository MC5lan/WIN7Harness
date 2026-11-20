package tools

// 验证闭环的"看得懂"部分：编译器和测试框架的原始输出对模型并不友好，
// 一两百行日志里真正有用的只有那几行 file:line。
//
// 这里把常见工具链的报错统一抽成 -> (文件, 行, 列, 严重度, 消息)，
// 去重后按文件聚合，放在报告最前面。模型拿到的第一屏就是"哪里错了"，
// 而不是先读完 60 行噪声再自己找。

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Diagnostic 一条可定位的报错。
type Diagnostic struct {
	File string
	Line int
	Col  int
	Sev  string // error / warning / fail
	Msg  string
}

var (
	// Go: main.go:12:3: undefined: foo   或   main.go:12: undefined: foo
	reDiagGo = regexp.MustCompile(`^([^\s:]+\.go):(\d+):(?:(\d+):)?\s*(.+)$`)
	// TypeScript: src/a.ts(12,3): error TS2304: Cannot find name 'x'
	reDiagTS = regexp.MustCompile(`^([^\s(]+\.\w+)\((\d+),(\d+)\):\s*(error|warning)\s+([A-Z]+\d+:\s*)?(.+)$`)
	// ESLint: src/a.js: line 12, col 3, Error - 'x' is not defined
	reDiagESLint = regexp.MustCompile(`^([^\s:]+):\s*line\s+(\d+),\s*col\s+(\d+),\s*(Error|Warning)\s*-\s*(.+)$`)
	// Python traceback: File "app/main.py", line 42, in <module>
	reDiagPyFile = regexp.MustCompile(`^\s*File "([^"]+)", line (\d+)`)
	// Python: src/a.py:12: SyntaxError: ...
	reDiagPy = regexp.MustCompile(`^([^\s:]+\.pyi?):(\d+):\s*(.+)$`)
	// Cargo/rustc 指向行： --> src/main.rs:12:3
	reDiagRust = regexp.MustCompile(`^\s*-->\s+([^\s:]+):(\d+):(\d+)`)
	// 通用兜底：path:12:3: error: msg / path:12: error msg
	reDiagGeneric = regexp.MustCompile(`^([^\s:]+):(\d+):(?:(\d+):)?\s*(error|Error|warning|Warning|FAIL|fail)[:：]?\s*(.+)$`)
	// Go 测试失败名：--- FAIL: TestFoo (0.01s)
	reDiagTestFail = regexp.MustCompile(`^---\s+FAIL:\s+(\S+)`)
)

const diagMax = 12

// ParseDiagnostics 从构建/测试输出里抽取可定位的报错，按文件聚合去重。
// framework 只用于决定优先匹配哪套规则，匹配不上会继续走通用规则。
func ParseDiagnostics(output, framework string) []Diagnostic {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	seen := make(map[string]bool, 16)
	out := make([]Diagnostic, 0, diagMax)

	add := func(d Diagnostic) bool {
		if d.Msg == "" {
			return false
		}
		d.Msg = strings.TrimSpace(d.Msg)
		if len(d.Msg) > 200 {
			d.Msg = cutMiddle(d.Msg, 200)
		}
		key := fmt.Sprintf("%s|%d|%s", d.File, d.Line, d.Msg)
		if seen[key] {
			return false
		}
		seen[key] = true
		out = append(out, d)
		return len(out) >= diagMax
	}

	for _, raw := range lines {
		ln := strings.TrimRight(raw, " \t")
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if strings.HasPrefix(ln, "go: ") || strings.Contains(ln, "downloading") {
			continue
		}
		if m := reDiagTS.FindStringSubmatch(ln); m != nil {
			if add(Diagnostic{File: m[1], Line: atoi(m[2]), Col: atoi(m[3]), Sev: m[4], Msg: m[6]}) {
				break
			}
			continue
		}
		if m := reDiagESLint.FindStringSubmatch(ln); m != nil {
			sev := "warning"
			if strings.EqualFold(m[4], "Error") {
				sev = "error"
			}
			if add(Diagnostic{File: m[1], Line: atoi(m[2]), Col: atoi(m[3]), Sev: sev, Msg: m[5]}) {
				break
			}
			continue
		}
		if m := reDiagGo.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			// Go 的 "main.go:12:3: msg" 与 "main.go:12: msg" 两种形态
			if add(Diagnostic{File: m[1], Line: atoi(m[2]), Col: atoi(m[3]), Sev: "error", Msg: m[4]}) {
				break
			}
			continue
		}
		if m := reDiagPyFile.FindStringSubmatch(ln); m != nil {
			// traceback 的报错类型在最后一行，这里只说"哪一行炸了"，
			// 完整异常由 verify 原文补上，避免把半句话当结论回灌模型。
			if add(Diagnostic{File: m[1], Line: atoi(m[2]), Sev: "error", Msg: "运行/测试中断于此行（异常类型见原文末行）"}) {
				break
			}
			continue
		}
		if m := reDiagPy.FindStringSubmatch(ln); m != nil {
			if add(Diagnostic{File: m[1], Line: atoi(m[2]), Sev: "error", Msg: m[3]}) {
				break
			}
			continue
		}
		if m := reDiagRust.FindStringSubmatch(ln); m != nil {
			if add(Diagnostic{File: m[1], Line: atoi(m[2]), Col: atoi(m[3]), Sev: "error", Msg: "编译错误（详见上方原文）"}) {
				break
			}
			continue
		}
		if m := reDiagTestFail.FindStringSubmatch(ln); m != nil {
			if add(Diagnostic{File: "(测试)", Sev: "fail", Msg: "用例失败：" + m[1]}) {
				break
			}
			continue
		}
		if m := reDiagGeneric.FindStringSubmatch(ln); m != nil {
			sev := "error"
			if strings.EqualFold(m[4], "warning") {
				sev = "warning"
			} else if strings.EqualFold(m[4], "fail") {
				sev = "fail"
			}
			if add(Diagnostic{File: m[1], Line: atoi(m[2]), Col: atoi(m[3]), Sev: sev, Msg: m[5]}) {
				break
			}
		}
	}
	_ = framework
	return out
}

// FormatDiagnostics 把诊断渲染成给模型看的短块：按文件聚合 + 逐条定位。
func FormatDiagnostics(ds []Diagnostic) string {
	if len(ds) == 0 {
		return ""
	}
	byFile := make(map[string][]Diagnostic, 4)
	var order []string
	for _, d := range ds {
		if _, ok := byFile[d.File]; !ok {
			order = append(order, d.File)
		}
		byFile[d.File] = append(byFile[d.File], d)
	}
	sort.SliceStable(order, func(i, j int) bool { return len(byFile[order[i]]) > len(byFile[order[j]]) })

	var b strings.Builder
	fmt.Fprintf(&b, "【错误定位】%d 个文件，共 %d 条\n", len(order), len(ds))
	for _, f := range order {
		list := byFile[f]
		fmt.Fprintf(&b, "%s（%d 条）\n", f, len(list))
		for _, d := range list {
			pos := f
			if d.Line > 0 {
				pos = fmt.Sprintf("%s:%d", f, d.Line)
				if d.Col > 0 {
					pos = fmt.Sprintf("%s:%d:%d", f, d.Line, d.Col)
				}
			}
			fmt.Fprintf(&b, "  - %s %s\n", pos, d.Msg)
		}
	}
	return b.String()
}

// DiagnoseVerifyFailure 是 verify 与自检共用的入口：给定失败输出返回可回灌的定位块。
func DiagnoseVerifyFailure(output, framework string) (string, []Diagnostic) {
	ds := ParseDiagnostics(output, framework)
	return FormatDiagnostics(ds), ds
}

func atoi(s string) int {
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
