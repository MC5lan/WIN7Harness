package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"win7harness/internal/core"
)

func init() {
	register(Tool{
		Name: "run_tests",
		Desc: "跑项目测试并只回失败摘要：自动识别 go test / npm test / pytest / cargo test，" +
			"解析出失败的用例名、文件行号与断言信息，而不是把几百行原始输出全部塞进上下文。" +
			"改完代码后用它确认没有回归，比 run_command 跑测试更省 token。",
		Schema: `{
  "type":"object",
  "properties":{
    "cwd":{"type":"string","description":"项目目录，默认当前工作目录"},
    "framework":{"type":"string","description":"auto|go|npm|pytest|cargo，默认 auto 按目录文件识别"},
    "run_pattern":{"type":"string","description":"只跑匹配的测试（go 的 -run / pytest 的 -k / cargo 的位置参数），只允许字母数字及正则元字符"},
    "extra_args":{"type":"string","description":"兼容旧调用：仅允许 -run/-k/-count/-v/-q/-race/--release/--all/--verbosity/-p 及 = 形式的白名单参数；非法字符直接报错"},
    "timeout":{"type":"integer","description":"超时秒数，默认 300，最大 1800"},
    "full_output":{"type":"boolean","description":"为 true 时额外返回原始输出尾部（默认 false 只给摘要）"}
  }
}`,
		Run: runTests,
	})
}

// maxTestOutput 测试输出缓冲上限：测试输出常常几 MB，只留尾部意义不大，
// 但为了能抓全失败行，这里给到 1MB，超出后尾部继续保留（见 tailBuffer）。
const maxTestOutput = 1 << 20

func runTests(ctx *Context, args map[string]interface{}) (Result, error) {
	cwd := core.ResolvePath(ctx.WorkDir, Get(args, "cwd"))
	if cwd == "" {
		cwd = ctx.WorkDir
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return Result{}, fmt.Errorf("目录不存在: %s", cwd)
	}

	fw := strings.ToLower(strings.TrimSpace(Get(args, "framework")))
	if fw == "" || fw == "auto" {
		fw = detectFramework(cwd)
	}
	if fw == "go" {
		if tr := CheckGoToolchain(cwd); !tr.OK() {
			return Result{Output: tr.Reason(), Display: true, Title: "测试（环境不支持）"}, nil
		}
	}
	extra := strings.TrimSpace(Get(args, "extra_args"))
	if rp := strings.TrimSpace(Get(args, "run_pattern")); rp != "" {
		// 结构化参数：按框架翻译成对应形式，字符白名单校验兜底。
		switch fw {
		case "go":
			extra = "-run " + rp
		case "pytest":
			extra = "-k " + rp
		case "cargo":
			extra = rp
		default:
			extra = rp
		}
	}
	clean, err := sanitizeExtraArgs(extra)
	if err != nil {
		return Result{}, fmt.Errorf("测试参数不合法：%v", err)
	}
	cmdline, ok := testCommand(fw, clean, "")
	if !ok {
		return Result{}, fmt.Errorf("无法识别测试框架：目录里没有 go.mod / package.json(test 脚本) / pytest 配置 / Cargo.toml，可用 framework 显式指定")
	}

	timeout := GetInt(args, "timeout", 300)
	if timeout <= 0 {
		timeout = 300
	}
	if timeout > 1800 {
		timeout = 1800
	}
	if ctx.Progress != nil {
		ctx.Progress("$ " + cmdline)
	}

	out, code, err := runShell(cmdline, cwd, time.Duration(timeout)*time.Second, nil, ctxDone(ctx))
	if err != nil && out == "" {
		return Result{}, err
	}
	sum := parseTestOutput(fw, out, code)
	text := sum.render(fw, cmdline, code, out, GetBool(args, "full_output", false))
	return Result{Output: text, Title: "测试"}, err
}

// detectFramework 按目录里的项目文件猜测试框架，猜不到返回空。
func detectFramework(dir string) string {
	if fileExists(filepath.Join(dir, "go.mod")) {
		return "go"
	}
	if fileExists(filepath.Join(dir, "Cargo.toml")) {
		return "cargo"
	}
	if p := filepath.Join(dir, "package.json"); fileExists(p) {
		if raw, err := os.ReadFile(p); err == nil {
			var pkg struct {
				Scripts map[string]string `json:"scripts"`
			}
			if json.Unmarshal(raw, &pkg) == nil {
				if _, ok := pkg.Scripts["test"]; ok {
					return "npm"
				}
			}
		}
	}
	for _, name := range []string{"pytest.ini", "conftest.py", "tox.ini", "setup.cfg", "pyproject.toml"} {
		if fileExists(filepath.Join(dir, name)) {
			return "pytest"
		}
	}
	if fileExists(filepath.Join(dir, "tests")) || fileExists(filepath.Join(dir, "test")) {
		return "pytest"
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// testCommand 把框架映射成命令行。
func testCommand(fw, extra, pkgArgs string) (string, bool) {
	var base string
	switch fw {
	case "go":
		if strings.TrimSpace(pkgArgs) != "" {
			if quoted, ok := quotePackageArgs(pkgArgs); ok {
				base = "go test " + quoted
			} else {
				base = "go test ./..."
			}
		} else {
			base = "go test ./..."
		}
	case "npm":
		base = "npm test"
	case "pytest":
		base = "python -m pytest -q"
	case "cargo":
		base = "cargo test"
	default:
		return "", false
	}
	if extra != "" {
		switch fw {
		case "npm":
			// npm 需要在 -- 之后透传给底层测试框架
			base += " -- " + extra
		default:
			base += " " + extra
		}
	}
	return base, true
}

// sanitizeExtraArgs 校验要拼进测试命令的参数。
// 只放行字母数字与正则元字符（-k/-run 的值需要）；每个 token 在拼接前
// 还会被 shell 引用，避免 | & 等被 cmd.exe 当成管道/命令分隔符。
var extraArgToken = regexp.MustCompile(`^[A-Za-z0-9._/^$*+?\[\]()|:=-]+$`)

// allowedExtraFlags 允许出现的旗标（go/pytest/cargo 通用子集）。
var allowedExtraFlags = map[string]bool{
	"-run": true, "-k": true, "-count": true, "-v": true, "-q": true,
	"-race": true, "-p": true, "--count": true, "--run": true,
	"--verbosity": true, "--release": true, "--all": true, "--locked": true,
	"--verbose": true, "--ignore-fail-on-empty": true,
}

func sanitizeExtraArgs(extra string) (string, error) {
	if extra == "" {
		return "", nil
	}
	toks := strings.Fields(extra)
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		if !extraArgToken.MatchString(t) {
			return "", fmt.Errorf("参数包含不允许的字符：%q（只允许字母数字与 -_./^$*+?[]()|= 等）", t)
		}
		if !safeShellArg(t) {
			return "", fmt.Errorf("参数包含 shell 展开字符：%q", t)
		}
		lt := strings.ToLower(t)
		if strings.HasPrefix(t, "-") {
			flag := lt
			hasValue := false
			if i := strings.Index(lt, "="); i > 0 {
				flag, hasValue = lt[:i], true
			}
			if !allowedExtraFlags[flag] {
				return "", fmt.Errorf("不支持的参数：%q（只允许 -run/-k/-count/-v/-q/-race/-p/--count/--run/--verbosity/--release/--all 及 = 形式）", t)
			}
			_ = hasValue
		}
		out = append(out, quoteShellArg(t))
	}
	return strings.Join(out, " "), nil
}

// testSummary 解析后的测试结果。
type testSummary struct {
	Passed  int
	Failed  int
	Skipped int
	// Failures 失败用例，最多保留 20 条。
	Failures []testFailure
	// Notes 一行行补充说明（编译失败、无法解析等）。
	Notes []string
	// Broken 表示测试根本没跑起来（编译错误 / 依赖缺失），此时失败数为 0 也没意义。
	Broken bool
}

type testFailure struct {
	Name   string // 用例名
	Where  string // 包或文件
	Detail string // 断言 / 行号
}

var (
	reGoFail    = regexp.MustCompile(`^\s*--- FAIL: (\S+) \(([^)]*)\)`)
	reGoLoc     = regexp.MustCompile(`^\s*(\S+\.go:\d+):\s*(.*)$`)
	reGoPkgFail = regexp.MustCompile(`^(?:FAIL|---\s+FAIL)\s+(\S+)\s*(?:\[build failed\])?`)
	reGoPkgOK   = regexp.MustCompile(`^ok\s+\S+`)
	rePyFail    = regexp.MustCompile(`^(?:FAILED|ERROR)\s+(\S+?)(?:\s+-\s+(.*))?$`)
	rePyStat    = regexp.MustCompile(`(\d+) failed|(\d+) passed|(\d+) skipped|(\d+) error`)
	reJestFail  = regexp.MustCompile(`^\s*(?:✕|×|●)\s+(.+?)(?:\s+\(\d+\s*ms\))?$`)
	reJestStat  = regexp.MustCompile(`Tests:\s+(.+)$`)
	reCargoFail = regexp.MustCompile(`^test (\S+) \.\.\. FAILED`)
	reCargoStat = regexp.MustCompile(`^test result: (\w+)\. (\d+) passed; (\d+) failed`)
	reBuildBad  = regexp.MustCompile(`(?m)^(?:#\s+\S+|\S+\.go:\d+:\d+:.*|.*cannot find package.*|.*undefined:.*)$`)
)

// parseTestOutput 按框架解析输出。
func parseTestOutput(fw, out string, code int) testSummary {
	var s testSummary
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	switch fw {
	case "go":
		var cur *testFailure
		for _, ln := range lines {
			if m := reGoFail.FindStringSubmatch(ln); m != nil {
				s.Failed++
				f := testFailure{Name: m[1]}
				if len(s.Failures) < 20 {
					s.Failures = append(s.Failures, f)
					cur = &s.Failures[len(s.Failures)-1]
				} else {
					cur = nil
				}
				continue
			}
			if cur != nil && strings.TrimSpace(cur.Detail) == "" {
				if m := reGoLoc.FindStringSubmatch(ln); m != nil {
					cur.Detail = strings.TrimSpace(m[1] + " " + m[2])
				}
			}
			if m := reGoPkgFail.FindStringSubmatch(ln); m != nil && strings.Contains(ln, "build failed") {
				s.Broken = true
				s.Notes = append(s.Notes, "包编译失败："+m[1])
			}
			if reGoPkgOK.MatchString(ln) {
				s.Passed++
			}
		}
		// 包级 ok 的行统计的是包数，这里换算成"通过包数"更直观
		if s.Passed > 0 {
			s.Notes = append(s.Notes, fmt.Sprintf("通过 %d 个包", s.Passed))
		}
		if s.Failed == 0 && code != 0 && reBuildBad.MatchString(out) {
			s.Broken = true
			s.Notes = append(s.Notes, "测试未能开始：编译或依赖错误")
			for _, ln := range firstMatches(out, reBuildBad, 5) {
				s.Notes = append(s.Notes, "  "+strings.TrimSpace(ln))
			}
		}

	case "pytest":
		var cur *testFailure
		for _, ln := range lines {
			t := strings.TrimSpace(ln)
			if m := rePyFail.FindStringSubmatch(t); m != nil {
				s.Failed++
				f := testFailure{Name: m[1], Detail: strings.TrimSpace(m[2])}
				if len(s.Failures) < 20 {
					s.Failures = append(s.Failures, f)
					cur = &s.Failures[len(s.Failures)-1]
				} else {
					cur = nil
				}
				continue
			}
			if cur != nil && cur.Detail == "" && strings.Contains(t, "assert") {
				cur.Detail = t
			}
			if m := rePyStat.FindAllStringSubmatch(t, -1); m != nil {
				for _, g := range m {
					n := atoiSafe(g[1])
					switch {
					case g[1] != "":
						s.Failed = maxInt(s.Failed, n)
					case g[2] != "":
						s.Passed = maxInt(s.Passed, atoiSafe(g[2]))
					case g[3] != "":
						s.Skipped = maxInt(s.Skipped, atoiSafe(g[3]))
					}
				}
			}
			if strings.Contains(t, "error: ") && (strings.Contains(t, "ModuleNotFoundError") || strings.Contains(t, "ImportError")) {
				s.Broken = true
				s.Notes = append(s.Notes, t)
			}
		}

	case "npm":
		for _, ln := range lines {
			t := strings.TrimSpace(ln)
			if m := reJestFail.FindStringSubmatch(ln); m != nil {
				s.Failed++
				if len(s.Failures) < 20 {
					s.Failures = append(s.Failures, testFailure{Name: strings.TrimSpace(m[1])})
				}
			}
			if m := reJestStat.FindStringSubmatch(t); m != nil {
				stat := m[1]
				s.Failed = maxInt(s.Failed, numBefore(stat, "failed"))
				s.Passed = maxInt(s.Passed, numBefore(stat, "passed"))
				s.Skipped = maxInt(s.Skipped, numBefore(stat, "skipped"))
			}
			if strings.Contains(t, "Cannot find module") || strings.Contains(t, "npm ERR! missing script") {
				s.Broken = true
				s.Notes = append(s.Notes, t)
			}
		}

	case "cargo":
		for _, ln := range lines {
			t := strings.TrimSpace(ln)
			if m := reCargoFail.FindStringSubmatch(t); m != nil {
				s.Failed++
				if len(s.Failures) < 20 {
					s.Failures = append(s.Failures, testFailure{Name: m[1]})
				}
			}
			if m := reCargoStat.FindStringSubmatch(t); m != nil {
				s.Passed += atoiSafe(m[2])
				s.Failed += 0
				if atoiSafe(m[3]) > 0 && s.Failed == 0 {
					s.Failed = atoiSafe(m[3])
				}
			}
			if strings.HasPrefix(t, "error[") || strings.HasPrefix(t, "error:") {
				s.Broken = true
				if len(s.Notes) < 3 {
					s.Notes = append(s.Notes, t)
				}
			}
		}
	}

	if code != 0 && s.Failed == 0 && len(s.Notes) == 0 {
		s.Notes = append(s.Notes, "退出码非 0 但没有解析到失败用例，可能是编译错误或环境问题，建议 full_output=true 看原始输出")
	}
	return s
}

func (s testSummary) render(fw, cmdline string, code int, raw string, full bool) string {
	var b strings.Builder
	sum := "通过"
	if s.Failed > 0 || (code != 0 && !s.Broken) {
		sum = "失败"
	}
	if s.Broken {
		sum = "未能跑起来"
	}
	fmt.Fprintf(&b, "框架: %s\n命令: %s\n结论: %s（退出码 %d）\n", fw, cmdline, sum, code)
	if s.Passed > 0 || s.Failed > 0 || s.Skipped > 0 {
		fmt.Fprintf(&b, "统计: 通过 %d / 失败 %d / 跳过 %d\n", s.Passed, s.Failed, s.Skipped)
	}
	for _, n := range s.Notes {
		b.WriteString("说明: " + n + "\n")
	}

	if len(s.Failures) > 0 {
		b.WriteString("\n失败用例:\n")
		for i, f := range s.Failures {
			line := fmt.Sprintf("  %d. %s", i+1, f.Name)
			if f.Where != "" {
				line += "（" + f.Where + "）"
			}
			if f.Detail != "" {
				line += " — " + truncateRunes(f.Detail, 200)
			}
			b.WriteString(line + "\n")
		}
		if s.Failed > len(s.Failures) {
			fmt.Fprintf(&b, "  …另有 %d 条未列出\n", s.Failed-len(s.Failures))
		}
	}

	if s.Failed > 0 || s.Broken {
		if tail := tailLines(raw, 30); tail != "" && !full {
			b.WriteString("\n输出尾部（30 行）:\n" + tail)
		}
	}
	if full {
		b.WriteString("\n完整原始输出:\n" + tailLines(raw, 200))
	}
	return b.String()
}

// tailLines 取末尾 n 行，压缩空行。
func tailLines(raw string, n int) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]string, 0, len(lines))
	blank := 0
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// numBefore 从 "1 failed, 5 passed" 这类片段里取出某个词前面的数字。
func numBefore(stat, word string) int {
	idx := strings.Index(stat, word)
	if idx < 0 {
		return 0
	}
	i := idx - 1
	for i >= 0 && (stat[i] == ' ' || stat[i] == ',') {
		i--
	}
	end := i + 1
	for i >= 0 && stat[i] >= '0' && stat[i] <= '9' {
		i--
	}
	return atoiSafe(stat[i+1 : end])
}

func firstMatches(s string, re *regexp.Regexp, n int) []string {
	all := re.FindAllString(s, -1)
	if len(all) > n {
		all = all[:n]
	}
	return all
}
