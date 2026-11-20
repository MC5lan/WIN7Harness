package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"win7harness/internal/core"
)

// Win7 的硬上限。这些不是“建议版本”，而是最后一个能在目标系统启动的版本：
//   - Go 1.21 起官方不再支持 Win7；
//   - gopls v0.16 起要求用 Go 1.21+ 构建，官方二进制在 Win7 上起不来；
//   - Git for Windows 2.47 起依赖新版 MSYS2/Cygwin，Win7 不支持。
const (
	win7MaxGoVersion    = "1.20.14"
	win7MaxGoplsVersion = "0.15.3"
)

// ToolchainReport 是一次“当前 OS + 项目 go.mod”的兼容性结论。
// 同一套判断在 Win7 与 Win10/11 上给出不同结果：Win7 锁定上限，
// 新系统优先用本机更新的工具链，不强行降级。
type ToolchainReport struct {
	OS        string
	Arch      string
	OSVersion string
	Win7      bool

	GoVersion string
	GoPath    string
	Gopls     string
	Git       string

	GoMod              string
	GoDirective        string
	ToolchainDirective string
	GoWorkDirective    string
	LocalBuild         bool
	Problems           []string
	Warnings           []string
	Notes              []string
}

// OK 表示本机可以尝试构建/测试。警告不影响执行。
func (r ToolchainReport) OK() bool { return r.LocalBuild && len(r.Problems) == 0 }

// Reason 给模型/用户看的一句话结论，避免把“环境不支持”误判成“代码没改对”。
func (r ToolchainReport) Reason() string {
	if len(r.Problems) == 0 {
		return "工具链满足当前项目要求，可以在本机构建/测试。"
	}
	var b strings.Builder
	b.WriteString("本机工具链不满足项目要求，未执行构建/测试：\n")
	for _, p := range r.Problems {
		b.WriteString("- " + p + "\n")
	}
	b.WriteString("建议：")
	switch {
	case r.Win7:
		b.WriteString("Win7 本机最高 Go 1.20.14；如果项目要求更高，请改用 Win10/11 或远程构建机，或把项目 go.mod 降到 1.20。")
	default:
		b.WriteString("升级本机 Go/gopls，或把项目要求降到本机可用版本。")
	}
	return b.String()
}

func (r ToolchainReport) Summary() string {
	var b strings.Builder
	osName := r.OS
	if r.Win7 {
		osName += "（Win7 基线）"
	}
	fmt.Fprintf(&b, "系统：%s / %s（%s）\n", r.OSVersion, r.Arch, osName)
	if r.GoVersion == "" {
		b.WriteString("Go：未找到（PATH 里没有可用的 go）\n")
	} else {
		fmt.Fprintf(&b, "Go：%s（%s）\n", r.GoVersion, r.GoPath)
	}
	if r.GoMod != "" {
		fmt.Fprintf(&b, "go.mod：go %s", r.GoDirective)
		if r.ToolchainDirective != "" {
			fmt.Fprintf(&b, "；toolchain %s", r.ToolchainDirective)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("go.mod：未找到（非 Go 项目）\n")
	}
	if r.GoWorkDirective != "" {
		fmt.Fprintf(&b, "go.work：go %s\n", r.GoWorkDirective)
	}
	if r.Gopls == "" {
		b.WriteString("gopls：未找到\n")
	} else {
		fmt.Fprintf(&b, "gopls：%s\n", r.Gopls)
	}
	if r.Git == "" {
		b.WriteString("git：未找到\n")
	} else {
		fmt.Fprintf(&b, "git：%s\n", r.Git)
	}
	for _, n := range r.Notes {
		b.WriteString("提示：" + n + "\n")
	}
	for _, w := range r.Warnings {
		b.WriteString("警告：" + w + "\n")
	}
	if r.OK() {
		b.WriteString("结论：本机可以构建/测试。\n")
	} else {
		b.WriteString("结论：本机不能构建/测试。\n")
		for _, p := range r.Problems {
			b.WriteString("- " + p + "\n")
		}
		b.WriteString(r.Reason())
	}
	return b.String()
}

// CheckGoToolchain 只探测 Go 与 go.mod/go.work，供 verify/run_tests/diagnostics
// 使用：这些路径不需要为了版本判断去启动 gopls，老机器上能省一次进程启动。
func CheckGoToolchain(root string) ToolchainReport {
	r := ToolchainReport{
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		OSVersion:  core.OSVersion(),
		LocalBuild: true,
	}
	r.Win7 = osIsWin7(r.OS, r.OSVersion)

	r.GoPath = goToolPath()
	r.GoVersion = probeVersion(goVersionCommand(), `go([0-9]+\.[0-9]+(?:\.[0-9]+)?)`, root, 5*time.Second)
	if r.GoVersion == "" && r.GoPath != "" {
		r.Warnings = append(r.Warnings, "找到 go 但执行 `go version` 失败，可能是损坏/不兼容的安装")
	}

	if root != "" {
		mod := filepath.Join(root, "go.mod")
		if _, err := os.Stat(mod); err == nil {
			r.GoMod = mod
			r.GoDirective, r.ToolchainDirective = readGoModDirectives(mod)
		}
		work := filepath.Join(root, "go.work")
		if _, err := os.Stat(work); err == nil {
			r.GoWorkDirective, _ = readGoModDirectives(work)
		}
	}

	evaluateToolchainPolicy(&r)
	return r
}

// CheckToolchain 是完整报告：在 Go 预检之外再看 gopls/git。
// root 为空时只报告本机工具。
func CheckToolchain(root string) ToolchainReport {
	r := CheckGoToolchain(root)
	r.Gopls = probeVersion("gopls version", `v([0-9]+\.[0-9]+\.[0-9]+)`, root, 5*time.Second)
	r.Git = probeVersion("git --version", `git version ([0-9]+(?:\.[0-9]+)+)`, root, 5*time.Second)
	applyGoplsPolicy(&r)
	return r
}

// evaluateToolchainPolicy 是纯逻辑部分，方便在 Win10/11 开发机上
// 用单测模拟 Win7，不需要真的装一台 Win7。
func evaluateToolchainPolicy(r *ToolchainReport) {
	r.LocalBuild = true
	hasGoMod := r.GoMod != "" || r.GoWorkDirective != ""
	if !hasGoMod {
		if r.GoVersion == "" {
			r.Notes = append(r.Notes, "当前目录不是 Go 项目；只报告本机工具版本")
		}
		return
	}

	need := highestGoDirective(r.GoDirective, r.ToolchainDirective, r.GoWorkDirective)

	if r.Win7 {
		if r.GoVersion == "" {
			r.LocalBuild = false
			r.Problems = append(r.Problems, "Win7 上未找到 go；本机最高支持 Go "+win7MaxGoVersion)
		} else if cmpVersion(r.GoVersion, win7MaxGoVersion) > 0 {
			r.LocalBuild = false
			r.Problems = append(r.Problems, fmt.Sprintf("当前 Go %s 高于 Win7 上限 %s", r.GoVersion, win7MaxGoVersion))
		}
		if need != "" && goMinorGreater(need, "1.20") {
			r.LocalBuild = false
			r.Problems = append(r.Problems, fmt.Sprintf("项目要求 Go %s，但 Win7 本机最高只能运行 Go %s", need, win7MaxGoVersion))
		}
		applyGoplsPolicy(r)
		return
	}

	// Win10/11 或 Linux：优先用本机工具链，不强行降级。
	if r.GoVersion == "" {
		r.LocalBuild = false
		r.Problems = append(r.Problems, "PATH 里没有找到 go")
		return
	}
	if need != "" && cmpVersion(need, r.GoVersion) > 0 {
		if cmpVersion(r.GoVersion, "1.21") >= 0 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("项目要求 Go %s，本机是 %s；Go 1.21+ 默认可用 GOTOOLCHAIN=auto 自动下载，需要网络", need, r.GoVersion))
		} else {
			r.LocalBuild = false
			r.Problems = append(r.Problems, fmt.Sprintf("项目要求 Go %s，本机是 %s；当前 Go 不支持工具链自动下载，请升级", need, r.GoVersion))
		}
	}
}

func applyGoplsPolicy(r *ToolchainReport) {
	if r == nil || !r.Win7 || r.Gopls == "" {
		return
	}
	if cmpVersion(r.Gopls, win7MaxGoplsVersion) > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("gopls %s 高于 Win7 可用上限 v%s；官方新二进制在 Win7 上可能无法启动，建议锁定 v%s", r.Gopls, win7MaxGoplsVersion, win7MaxGoplsVersion))
	}
}

func highestGoDirective(dirs ...string) string {
	best := ""
	for _, d := range dirs {
		d = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(d), "go"))
		if d == "" || !regexp.MustCompile(`^[0-9]+(\.[0-9]+){1,2}$`).MatchString(d) {
			continue
		}
		if best == "" || cmpVersion(d, best) > 0 {
			best = d
		}
	}
	return best
}

func readGoModDirectives(path string) (goVer, toolchain string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "go":
			if goVer == "" {
				goVer = fields[1]
			}
		case "toolchain":
			if toolchain == "" {
				toolchain = strings.TrimPrefix(fields[1], "go")
			}
		}
	}
	return goVer, toolchain
}

// goToolPath 优先用 W7H_GO：开发机/绿色版经常没有把 Go 放进 PATH，
// 但测试和构建仍然需要一个明确的 go.exe。
func goToolPath() string {
	if v := strings.TrimSpace(os.Getenv("W7H_GO")); v != "" {
		return v
	}
	if p, err := exec.LookPath("go"); err == nil {
		return p
	}
	return ""
}

func goVersionCommand() string {
	p := goToolPath()
	if p == "" {
		return "go version"
	}
	if strings.ContainsAny(p, " \t") {
		return `"` + p + `" version`
	}
	return p + " version"
}

func probeVersion(cmdline, pattern, dir string, timeout time.Duration) string {
	if dir == "" {
		dir = "."
	}
	out, code, err := runShell(cmdline, dir, timeout, nil)
	if err != nil || code != 0 {
		return ""
	}
	re := regexp.MustCompile(pattern)
	if m := re.FindStringSubmatch(out); len(m) > 1 {
		return m[1]
	}
	return ""
}

func osIsWin7(goos, ver string) bool {
	if goos != "windows" {
		return false
	}
	// core.OSVersion 返回 "Windows 6.1.7601"；只认 6.1（Win7/Server 2008 R2）。
	return strings.HasPrefix(ver, "Windows 6.1.")
}

// goMinorGreater 只比较 major.minor：go.mod 里的 go 1.20.14 仍然是
// Win7 的 1.20 语言版本，不能因为 patch 号比 "1.20" 大就误判。
func goMinorGreater(a, b string) bool {
	pa, oka := parseVersionTriple(a)
	pb, okb := parseVersionTriple(b)
	if !oka || !okb {
		return false
	}
	if pa[0] != pb[0] {
		return pa[0] > pb[0]
	}
	return pa[1] > pb[1]
}

func cmpVersion(a, b string) int {
	pa, oka := parseVersionTriple(a)
	pb, okb := parseVersionTriple(b)
	if !oka || !okb {
		return strings.Compare(a, b)
	}
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func parseVersionTriple(s string) ([3]int, bool) {
	var out [3]int
	re := regexp.MustCompile(`^v?([0-9]+)(?:\.([0-9]+))?(?:\.([0-9]+))?`)
	m := re.FindStringSubmatch(strings.TrimSpace(s))
	if len(m) == 0 {
		return out, false
	}
	for i := 1; i <= 3; i++ {
		if i < len(m) && m[i] != "" {
			n, err := strconv.Atoi(m[i])
			if err != nil {
				return out, false
			}
			out[i-1] = n
		}
	}
	return out, true
}

func init() {
	register(Tool{
		Name: "toolchain",
		Desc: "检查本机 Go/gopls/Git 版本与项目 go.mod/go.work 的要求是否匹配。Win7 上会明确告诉你仓库是否超出本机工具链上限，避免反复重试无法完成的构建/测试。",
		Schema: `{
  "type":"object",
  "properties":{
    "path":{"type":"string","description":"项目目录，默认当前工作目录"}
  }
}`,
		Run: runToolchain,
	})
}

func runToolchain(ctx *Context, args map[string]interface{}) (Result, error) {
	root := core.ResolvePath(ctx.WorkDir, Get(args, "path"))
	if root == "" {
		root = ctx.WorkDir
	}
	if err := guardRead(ctx, root, "工具链检查"); err != nil {
		return Result{}, err
	}
	rep := CheckToolchain(root)
	return Result{Output: rep.Summary(), Display: true, Title: "工具链预检"}, nil
}
