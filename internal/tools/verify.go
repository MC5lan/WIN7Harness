package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"win7harness/internal/core"
)

// 验证闭环的执行端：改完代码后自动跑一遍"构建 + 测试"，把失败摘要交回模型继续修。
//
// 这里刻意复用 run_tests 的框架识别与输出解析，而不是另写一套：
// 同一套规则既能回答"这个项目怎么测"，也能保证自动验证与用户手动跑的结论一致。

// VerifyResult 是一次自动验证的结果。
type VerifyResult struct {
	OK        bool          // 构建与测试都通过
	Framework string        // 识别出的框架
	BuildCmd  string        // 构建命令（可能为空：Python 这类没有独立构建）
	TestCmd   string        // 测试命令
	Skipped   string        // 未执行的原因（识别不到框架、命令缺失等）
	Report    string        // 给模型看的精简报告
	Duration  time.Duration // 总耗时
	// Diags 是从失败输出里抽出的可定位错误，界面据此标"错在哪几行"。
	Diags []Diagnostic
}

// ctxDone 提取可选的取消通道：tools.Context.Ctx 为 nil 或未设置时返回 nil
// （runShell 忽略 nil 取消通道，行为与此前一致）。
func ctxDone(ctx *Context) <-chan struct{} {
	if ctx == nil || ctx.Ctx == nil {
		return nil
	}
	return ctx.Ctx.Done()
}

// VerifyProject 对目录做一次构建 + 测试验证。
// timeout <= 0 时按单步 300 秒。
func VerifyProject(ctx *Context, cwd, extraArgs string, timeout time.Duration) VerifyResult {
	return verifyProject(ctx, cwd, extraArgs, timeout, nil)
}

// VerifyProjectTargets 与 VerifyProject 相同，但可传入本轮改动文件；
// Go 项目会只跑受影响包的测试（构建仍全量，防止接口改动拖垮别的包）。
func VerifyProjectTargets(ctx *Context, cwd, extraArgs string, timeout time.Duration, changed []string) VerifyResult {
	return verifyProject(ctx, cwd, extraArgs, timeout, changed)
}

func verifyProject(ctx *Context, cwd, extraArgs string, timeout time.Duration, changed []string) VerifyResult {
	start := time.Now()
	if cwd == "" && ctx != nil {
		cwd = ctx.WorkDir
	}
	if cwd == "" {
		return VerifyResult{Skipped: "工作目录为空"}
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return VerifyResult{Skipped: fmt.Sprintf("目录不存在：%s", cwd)}
	}
	fw := detectFramework(cwd)
	if fw == "" {
		return VerifyResult{Skipped: "目录里没有可识别的测试框架（go.mod / package.json / Cargo.toml / pytest 配置）"}
	}
	if fw == "go" {
		tr := CheckGoToolchain(cwd)
		if !tr.OK() {
			// 环境不支持就不要再跑 300 秒构建/测试：模型会把必然失败误判成代码问题，
			// 然后在“改代码→重跑→再失败”的循环里空转。
			return VerifyResult{Skipped: "工具链不满足", Report: tr.Reason(), Duration: time.Since(start)}
		}
	}
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	pkgArgs := ""
	if fw == "go" {
		pkgArgs = expandGoTargets(cwd, changed)
	}
	res := VerifyResult{Framework: fw}

	// 1) 构建：语法错误、类型错误在这一步就暴露，比等测试跑完再看更省时间。
	if cmd := buildCommand(fw, cwd); cmd != "" {
		res.BuildCmd = cmd
		if ctx != nil && ctx.Progress != nil {
			ctx.Progress("$ " + cmd)
		}
		out, code, err := runShell(cmd, cwd, timeout, nil, ctxDone(ctx))
		if err != nil && out == "" {
			res.Report = trimVerifyReport(fmt.Sprintf("构建无法启动：%v", err))
			res.Duration = time.Since(start)
			return res
		}
		if code != 0 {
			block, ds := DiagnoseVerifyFailure(out, fw)
			res.Diags = ds
			res.Report = trimVerifyReport(joinDiagBlock(block,
				fmt.Sprintf("构建失败（退出码 %d）：\n%s", code, tailLines(out, 60))))
			res.Duration = time.Since(start)
			return res
		}
	}

	// 2) 测试
	cleanArgs, errA := sanitizeExtraArgs(strings.TrimSpace(extraArgs))
	if errA != nil {
		res.Report = trimVerifyReport(fmt.Sprintf("测试参数不合法：%v", errA))
		res.Duration = time.Since(start)
		return res
	}
	cmd, ok := testCommand(fw, cleanArgs, pkgArgs)
	if !ok {
		res.Skipped = "框架 " + fw + " 没有可用测试命令"
		res.Duration = time.Since(start)
		return res
	}
	res.TestCmd = cmd
	if ctx != nil && ctx.Progress != nil {
		ctx.Progress("$ " + cmd)
	}
	out, code, err := runShell(cmd, cwd, timeout, nil, ctxDone(ctx))
	if err != nil && out == "" {
		res.Report = trimVerifyReport(fmt.Sprintf("测试无法启动：%v", err))
		res.Duration = time.Since(start)
		return res
	}
	sum := parseTestOutput(fw, out, code)
	if code == 0 && sum.Failed == 0 {
		res.OK = true
		res.Report = fmt.Sprintf("验证通过：构建与测试全部成功（%s，共 %d 个用例）。", fw, sum.Passed)
	} else {
		block, ds := DiagnoseVerifyFailure(out, fw)
		res.Diags = ds
		res.Report = trimVerifyReport(joinDiagBlock(block, sum.render(fw, cmd, code, out, false)))
	}
	res.Duration = time.Since(start)
	return res
}

// goPackageArgs 把本轮改动的 Go 文件折算成 go test 的包参数。
// 任何非 Go 文件、go.mod/go.sum 或工作区之外的路径都返回空串，退回全量测试。
func goPackageArgs(cwd string, changed []string) string {
	if len(changed) == 0 {
		return ""
	}
	set := map[string]bool{}
	for _, f := range changed {
		f = strings.TrimSpace(f)
		if f == "" {
			return ""
		}
		p := f
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		rel, err := filepath.Rel(cwd, p)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ""
		}
		base := strings.ToLower(filepath.Base(rel))
		if base == "go.mod" || base == "go.sum" {
			return ""
		}
		if strings.ToLower(filepath.Ext(rel)) != ".go" {
			return ""
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		pkg := "."
		if dir != "." {
			pkg = "./" + dir
		}
		// 包路径会拼进 go test 命令行。目录名里的 & | ^ ( ) 等字符由
		// testCommand 统一做 shell 引用；% 在 cmd.exe 双引号内仍会展开，
		// 遇到就退回全量测试（go test ./...）。
		if !safeShellArg(pkg) {
			return ""
		}
		set[pkg] = true
	}
	if len(set) == 0 {
		return ""
	}
	list := make([]string, 0, len(set))
	for p := range set {
		list = append(list, p)
	}
	sort.Strings(list)
	return strings.Join(list, " ")
}

// expandGoTargets 在改动包的基础上加上反向依赖包。
// 改接口/公共函数时，只测自己所在包会漏掉依赖方的测试；这里用 go list 找
// 直接/间接 import 了改动包的包，最多扩 3 层，超过 40 个包则退回全量。
func expandGoTargets(cwd string, changed []string) string {
	base := goPackageArgs(cwd, changed)
	if base == "" {
		return ""
	}
	extra := goReverseDeps(cwd, strings.Fields(base))
	if extra == "" {
		return base
	}
	return base + " " + extra
}

// goToolCommand 允许测试/bench 注入绝对路径的 go（开发机上 go 不一定在 PATH）。
func goToolCommand() string {
	if v := strings.TrimSpace(os.Getenv("W7H_GO")); v != "" {
		return v
	}
	return "go"
}

func goReverseDeps(cwd string, targets []string) string {
	if len(targets) == 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, goToolCommand(), "list", "-f", `{{.ImportPath}}|{{.Dir}}|{{join .Deps " "}}`, "./...")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), childNetEnv()...)
	out, err := cmd.Output()
	if err != nil || len(out) > 2<<20 {
		return ""
	}
	type pkgDeps struct {
		dir  string
		deps []string
	}
	pkgs := map[string]pkgDeps{}
	dirToImport := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		imp, dir := parts[0], parts[1]
		pkgs[imp] = pkgDeps{dir: dir, deps: strings.Fields(parts[2])}
		dirToImport[strings.ToLower(filepath.Clean(dir))] = imp
	}
	targetSet := map[string]bool{}
	for _, t := range targets {
		p := filepath.Clean(filepath.Join(cwd, filepath.FromSlash(t)))
		if imp, ok := dirToImport[strings.ToLower(p)]; ok {
			targetSet[imp] = true
		}
	}
	if len(targetSet) == 0 {
		return ""
	}
	// 反向依赖最多扩 3 层，避免一次改动把全仓测试都拖进来。
	dependents := map[string]bool{}
	frontier := targetSet
	for depth := 0; depth < 3 && len(frontier) > 0; depth++ {
		next := map[string]bool{}
		for imp, pd := range pkgs {
			if targetSet[imp] || dependents[imp] {
				continue
			}
			for _, dep := range pd.deps {
				if frontier[dep] {
					dependents[imp] = true
					next[imp] = true
					break
				}
			}
		}
		frontier = next
	}
	if len(dependents) == 0 {
		return ""
	}
	var outArgs []string
	for imp := range dependents {
		dir := pkgs[imp].dir
		rel, err := filepath.Rel(cwd, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			outArgs = append(outArgs, ".")
		} else {
			outArgs = append(outArgs, "./"+rel)
		}
		if len(outArgs) >= 40 {
			return "" // 依赖面太大，直接全量更稳
		}
	}
	sort.Strings(outArgs)
	return strings.Join(outArgs, " ")
}

// buildCommand 返回该框架的构建命令；返回空串表示这个框架没有单独的构建步骤。
func buildCommand(fw, dir string) string {
	switch fw {
	case "go":
		return "go build ./..."
	case "cargo":
		return "cargo build"
	case "npm":
		// 有 build 脚本才跑：只有 test 脚本的项目直接构建会报错。
		if hasNpmScript(dir, "build") {
			return "npm run build"
		}
		return ""
	case "pytest":
		// Python 没有通用构建步骤，编译检查交给测试导入本身。
		return ""
	default:
		return ""
	}
}

// hasNpmScript 判断 package.json 里是否存在某个脚本。
func hasNpmScript(dir, name string) bool {
	p := filepath.Join(dir, "package.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	// 精确到 "name"\s*: 的粗匹配即可，避免为一个布尔判断引入完整 JSON 解析路径。
	s := string(raw)
	key := `"` + name + `"`
	i := strings.Index(s, key)
	if i < 0 {
		return false
	}
	rest := s[i+len(key):]
	j := strings.Index(rest, ":")
	if j < 0 || j > 8 {
		return false
	}
	return true
}

// trimVerifyReport 给回灌给模型的报告加上统一抬头，并限制体积。
// 自动验证最多跑 3 轮，报告太长会挤掉模型对任务的记忆。
func trimVerifyReport(s string) string {
	const max = 6000
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = cutMiddle(s, max)
	}
	return "【自动验证】\n" + s
}

// joinDiagBlock 把结构化定位放在原始输出前面：先看结论，再看证据。
func joinDiagBlock(block, body string) string {
	block = strings.TrimSpace(block)
	body = strings.TrimSpace(body)
	if block == "" {
		return body
	}
	if body == "" {
		return block
	}
	return block + "\n---\n" + body
}

// VerifySummary 生成一行人类可读的结论，供界面状态栏展示。
func (r VerifyResult) VerifySummary() string {
	switch {
	case r.Skipped != "":
		return "跳过自动验证：" + r.Skipped
	case r.OK:
		return fmt.Sprintf("自动验证通过（%s，%.1fs）", r.Framework, r.Duration.Seconds())
	default:
		hint := ""
		if len(r.Diags) > 0 {
			first := r.Diags[0]
			pos := first.File
			if first.Line > 0 {
				pos = fmt.Sprintf("%s:%d", first.File, first.Line)
			}
			hint = fmt.Sprintf("，定位到 %s 等 %d 处", pos, len(r.Diags))
		}
		return fmt.Sprintf("自动验证失败（%s，%.1fs%s），已把失败摘要交回模型", r.Framework, r.Duration.Seconds(), hint)
	}
}

// HumanVerifyHint 是把验证结果拼进上下文时用的固定说明，
// 单独抽出来便于自检核对措辞与 agent 侧引用保持一致。
const HumanVerifyHint = "以上是自动验证结果。请先修复报告里的失败项再继续，不要忽略失败直接结束任务；" +
	"如果失败与本次改动无关（历史遗留或环境问题），请明确说明理由后再收尾。"

var _ = core.HumanSize
