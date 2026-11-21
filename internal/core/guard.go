package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// RiskLevel 描述一次工具调用的风险等级。
type RiskLevel int

const (
	RiskSafe RiskLevel = iota
	RiskCaution
	RiskDanger
)

func (r RiskLevel) String() string {
	switch r {
	case RiskSafe:
		return "safe"
	case RiskCaution:
		return "caution"
	default:
		return "danger"
	}
}

// forbiddenRoots 系统关键目录，任何写操作一律拒绝。
// 首位是按 %SystemDrive%/%SystemRoot% 动态推断的根（缺省 c:）。
func forbiddenRoots() []string {
	sys := "c:"
	if r := strings.TrimSpace(getenv("SystemDrive")); r != "" {
		sys = strings.ToLower(strings.TrimSuffix(r, `\`))
	}
	return []string{
		sys + `\windows`, sys + `\program files`, sys + `\program files (x86)`,
		sys + `\programdata\microsoft`, sys + `\$recycle.bin`, sys + `\system volume information`,
		sys + `\boot`, sys + `\recovery`,
	}
}

var getenv = os.Getenv

// ResolvePath 把相对路径解析到工作目录下，并清理 . / .. 。
// 注意：清理只是规范化，并不阻止 ../ 跳出工作区，越界判断用 EscapesWorkDir。
func ResolvePath(workDir, p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, `"'`)
	if p == "" {
		return workDir
	}
	// 反斜杠只在 Windows 上是分隔符；非 Windows 下照抄会得到含 \ 的怪异文件名。
	if filepath.Separator == '\\' {
		p = strings.ReplaceAll(p, "/", `\`)
		if filepath.VolumeName(p) == "" && !strings.HasPrefix(p, `\`) {
			p = filepath.Join(workDir, p)
		}
	} else if !filepath.IsAbs(p) {
		p = filepath.Join(workDir, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// realPath 尽力把路径规范化为真实路径（解析 junction/符号链接）。
// 不存在的路径：逐级回退到存在的父目录解析后再拼接剩余部分，
// 保证“工作区内的不存在路径”判定仍然正确（写新建文件是常态）。
func realPath(p string) string {
	p = filepath.Clean(p)
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		return rp
	}
	rest := []string{}
	cur := p
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return p // 到根都没解析出来，放弃
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
		if rp, err := filepath.EvalSymlinks(cur); err == nil {
			r := rp
			for _, seg := range rest {
				r = filepath.Join(r, seg)
			}
			return filepath.Clean(r)
		}
	}
}

// EscapesWorkDir 判断 resolved 是否落在工作目录之外。
// workDir 为空视为不限制；不同盘符（filepath.Rel 报错）按越界处理。
// 两端都做一次 junction/符号链接解析——工作区里放一个 junction 指向 C:\Windows
// 再写 j\evil.dll，纯词法判断会放行（见 tools 审计 TL-P0-4）。
func EscapesWorkDir(workDir, resolved string) bool {
	if strings.TrimSpace(workDir) == "" || strings.TrimSpace(resolved) == "" {
		return false
	}
	r := resolved
	if !filepath.IsAbs(r) {
		// 相对路径一律相对工作区解析（此前按进程 CWD 解析，判定基错了）
		r = filepath.Join(workDir, r)
	}
	w, err1 := filepath.Abs(workDir)
	r, err2 := filepath.Abs(r)
	if err1 != nil || err2 != nil {
		return false
	}
	w, r = realPath(w), realPath(r)
	if runtime.GOOS == "windows" {
		w, r = strings.ToLower(w), strings.ToLower(r)
	}
	rel, err := filepath.Rel(w, r)
	if err != nil {
		return true
	}
	if rel == "." {
		return false
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// IsForbiddenPath 判断路径是否落在系统保护目录内。
// 先剥掉 \\?\ 与 \\.\ 这类设备前缀（字面前缀匹配的老后门），
// 再做 junction/符号链接解析（工作区里放一个 junction 指向 C:\Windows，
// 纯字面比较挡不住），最后按前缀比较。
func IsForbiddenPath(p string) bool {
	p = strings.TrimSpace(p)
	for _, pre := range []string{`\\?\`, `\\.\`} {
		if strings.HasPrefix(strings.ToLower(p), strings.ToLower(pre)) {
			p = p[len(pre):]
			break
		}
	}
	if strings.TrimSpace(p) == "" {
		return false
	}
	real := realPath(filepath.Clean(p))
	low := strings.ToLower(real)
	for _, root := range forbiddenRoots() {
		if low == root || strings.HasPrefix(low, root+`\`) {
			return true
		}
	}
	return false
}

// dangerPatterns 明显破坏性的命令片段（匹配前统一做 normalizeCmd 归一，
// 消除双空格/制表符/引号造成的绕过）。
var dangerPatterns = []string{
	"format ", "format.com", "diskpart", "fdisk",
	"rd /s", "rmdir /s", "del /f", "del /q", "del /s",
	"rm -rf", "rm -fr", "mkfs", "dd if=",
	"cipher /w", "vssadmin delete", "bcdedit",
	"reg delete", "reg add hklm", "net user", "net localgroup",
	"shutdown", "taskkill /f /im explorer",
	"takeown /f", "icacls /reset", "attrib -r -s -h /s",
	// 联接点/符号链接制造：本身不“危险”，但它是越界写入系统目录的跳板。
	"mklink", "fsutil", "junction ", "create-shortcut",
	// 编码/混淆执行：无法静态解读其意图，一律按破坏性处理。
	"-enc ", "-encodedcommand", "frombase64string", "certutil -decode", "certutil -urlcache",
}

// interpreterPatterns 解释器入口：脚本字符串直执行，子串黑名单挡不住，
// 至少抬到 caution 触发确认。
var interpreterPatterns = []string{
	"powershell", "pwsh", "python -c", "python3 -c", "node -e", "node --eval",
	"wmic", "mshta", "rundll32", "regsvr32", "msbuild", "ruby -e", "perl -e", "php -r",
}

// normalizeCmd 把命令文本归一化，消除最常见的绕过形态：
//   - 连续空白/制表符/换行 → 单个空格（"rm  -rf" == "rm -rf"）
//   - 去掉引号（"rm" "-rf" == "rm -rf"）
//   - 全角空格也按空白处理
//   - 小写化
func normalizeCmd(cmd string) string {
	low := strings.ToLower(cmd)
	low = strings.ReplaceAll(low, "　", " ")
	low = strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r' || r == '　':
			return ' '
		case r == '"' || r == '\'':
			return -1 // 去掉引号
		default:
			return r
		}
	}, low)
	return strings.Join(strings.Fields(low), " ")
}

// CommandRisk 依据命令文本粗略分级（宁可多拦，不可放过）。
func CommandRisk(cmd string) RiskLevel {
	low := normalizeCmd(strings.TrimSpace(cmd))
	if low == "" {
		return RiskSafe
	}
	for _, p := range dangerPatterns {
		if strings.Contains(low, normalizeCmd(p)) {
			return RiskDanger
		}
	}
	if destructiveBySwitches(low) {
		return RiskDanger
	}
	// 解释器直执行：至少 caution，提醒用户确认。
	for _, p := range interpreterPatterns {
		if strings.Contains(low, p) {
			return RiskCaution
		}
	}
	// 写操作类命令
	for _, p := range []string{"del ", "copy ", "move ", "ren ", "mkdir", "echo >", ">", "git push", "npm install", "go build"} {
		if strings.Contains(low, p) {
			return RiskCaution
		}
	}
	return RiskSafe
}

// LooksLikeWrite 判断工具名是否属于写类操作（用于 UI 展示"写入"标记）。
func LooksLikeWrite(tool string) bool {
	switch tool {
	case "write_file", "edit_file", "apply_patch", "run_command", "delete_path", "move_path":
		return true
	}
	return false
}

// HumanSize 把字节数格式化为人类可读。
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return itoa(n) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n)
	i := -1
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return trimFloat(v) + " " + units[i]
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func trimFloat(v float64) string {
	s := ""
	// 保留一位小数，避免引入 strconv 之外的依赖差异
	whole := int64(v)
	frac := int64((v - float64(whole)) * 10)
	if frac == 0 {
		s = itoa(whole)
	} else {
		s = itoa(whole) + "." + itoa(frac)
	}
	return s
}

// destructiveBySwitches 按“命令 + 开关集合”识别删除类命令。
//
// 为什么需要：dangerPatterns 是定序子串，cmd/rm 的开关却与顺序无关，
// "rd /q /s"、"rmdir /q /s"、"rm -r -f"、"erase /s /q" 都能绕过 "rd /s"、"rm -rf"
// 这类条目被判为 safe，auto 模式下会静默执行。这里把命令切段、取命令名，
// 再看开关里有没有对应的破坏性标志，与书写顺序无关。
// 入参 low 已经过 normalizeCmd（小写、去引号、空白归一）。
func destructiveBySwitches(low string) bool {
	for _, seg := range splitCmdSegments(low) {
		f := strings.Fields(seg)
		// cmd /c rd /q /s x：剥掉外层 cmd 再看真正的命令。
		for len(f) >= 2 && cmdBase(f[0]) == "cmd" {
			i := 1
			for i < len(f) && strings.HasPrefix(f[i], "/") && f[i] != "/c" && f[i] != "/k" {
				i++
			}
			if i >= len(f) {
				break
			}
			f = f[i+1:]
		}
		if len(f) == 0 {
			continue
		}
		args := f[1:]
		switch cmdBase(f[0]) {
		case "rd", "rmdir":
			if hasCmdSwitch(args, 's') {
				return true
			}
		case "del", "erase":
			// 与 dangerPatterns 中 del /f、del /q、del /s 保持一致；erase 是 del 的别名。
			if hasCmdSwitch(args, 'f') || hasCmdSwitch(args, 'q') || hasCmdSwitch(args, 's') {
				return true
			}
		case "rm":
			// 与 rm -rf / rm -fr 保持一致：递归 + 强制同时出现才算高危。
			if hasDashFlag(args, 'r', "--recursive") && hasDashFlag(args, 'f', "--force") {
				return true
			}
		}
	}
	return false
}

// splitCmdSegments 按 && || & | ; 把命令行切成独立命令。
func splitCmdSegments(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '&' || r == '|' || r == ';'
	})
}

// cmdBase 取命令名：去掉路径与 .exe/.com 后缀（c:\windows\system32\cmd.exe -> cmd）。
func cmdBase(tok string) string {
	if i := strings.LastIndexAny(tok, `\/`); i >= 0 && i < len(tok)-1 {
		tok = tok[i+1:]
	}
	tok = strings.TrimSuffix(tok, ".exe")
	tok = strings.TrimSuffix(tok, ".com")
	return tok
}

// hasCmdSwitch 判断 cmd 风格开关里是否含 /<c>，兼容 "/s /q" 与连写的 "/s/q"。
func hasCmdSwitch(args []string, c byte) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "/") {
			continue
		}
		for _, part := range strings.Split(a[1:], "/") {
			if len(part) > 0 && part[0] == c {
				return true
			}
		}
	}
	return false
}

// hasDashFlag 判断 POSIX 风格参数里是否有短标志 -<c>（可与其它短标志合写，如 -rf）或长标志 long。
// rm 的 -r 与 -R 等价，normalizeCmd 已统一小写。
func hasDashFlag(args []string, c byte, long string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == long {
			return true
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.IndexByte(a[1:], c) >= 0 {
			return true
		}
	}
	return false
}
