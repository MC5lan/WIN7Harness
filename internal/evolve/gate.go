package evolve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"win7harness/internal/eval"
)

// DefaultProtected 是自迭代默认不允许改的路径。
// 这些文件是“尺子”：候选如果能把它们改掉，就能把门禁调松，分数再高也没有意义。
var DefaultProtected = []string{
	"internal/eval",
	"internal/selftest",
	"internal/evolve",
	"cmd/evolve",
	// 这两个是评测/自检的入口；候选如果能把它们改成永远输出绿色，
	// 后面的 eval/selftest 门禁就形同虚设。
	"cmd/evalrun",
	"cmd/selftest_run",
	// bench 一旦作为门禁，fixture 与 runner 也是尺子，不能由候选改。
	"cmd/bench",
	"internal/bench",
	"build.sh",
	"go.mod",
	"go.sum",
	"scripts/check.py",
}

// GateConfig 是可信门禁的配置。
type GateConfig struct {
	Repo         string
	GoBin        string
	GofmtBin     string
	Protected    []string
	Timeout      time.Duration
	TestTimeout  time.Duration
	MaxDiffLines int
	MaxFiles     int
	// BenchEnabled 为 true 时，候选改动命中模型侧路径就跑一个真实 bench fixture。
	// 默认关闭：bench 要调用模型，成本比 base eval 高得多。
	BenchEnabled bool
	BenchTask    string
	BenchTimeout time.Duration
	// Progress 让 CLI 在长时间门禁里能看到当前步骤；可为 nil。
	Progress func(string)
}

// DefaultGate 是 Stage 0 的门禁实现：
// 受保护路径 → 格式 → vet → 测试 → 双架构构建 → 评测 → 自检 → 逐用例退步判定。
type DefaultGate struct {
	Config GateConfig
}

func (g DefaultGate) goBin() string {
	if strings.TrimSpace(g.Config.GoBin) != "" {
		return g.Config.GoBin
	}
	return "go"
}

func (g DefaultGate) gofmtBin() string {
	if strings.TrimSpace(g.Config.GofmtBin) != "" {
		return g.Config.GofmtBin
	}
	return "gofmt"
}

func (g DefaultGate) timeout() time.Duration {
	if g.Config.Timeout > 0 {
		return g.Config.Timeout
	}
	return 40 * time.Minute
}

func (g DefaultGate) testTimeout() time.Duration {
	if g.Config.TestTimeout > 0 {
		return g.Config.TestTimeout
	}
	return 20 * time.Minute
}

func (g DefaultGate) protected() []string {
	if len(g.Config.Protected) > 0 {
		return g.Config.Protected
	}
	return DefaultProtected
}

// Run 执行门禁。返回的 error 只表示门禁自身无法运行；
// 候选不合格通过 GateResult.OK=false 与 Failures 表达。
func (g DefaultGate) Run(ctx context.Context, in GateInput) (GateResult, error) {
	cctx, cancel := context.WithTimeout(ctx, g.timeout())
	defer cancel()

	res := GateResult{BaseEval: in.BaseEval}
	if in.BaseDir == "" || in.CandDir == "" {
		return res, fmt.Errorf("门禁缺少基线或候选目录")
	}
	ok := true
	step := func(name string, fn func() (string, error)) bool {
		if g.Config.Progress != nil {
			g.Config.Progress("开始 " + name)
		}
		t0 := time.Now()
		detail, err := fn()
		st := GateStep{Name: name, OK: err == nil, Detail: strings.TrimSpace(detail), Ms: time.Since(t0).Milliseconds()}
		if err != nil {
			st.OK = false
			st.Detail = strings.TrimSpace(st.Detail + "\n" + err.Error())
			res.Failures = append(res.Failures, name+"："+err.Error())
			ok = false
		}
		if g.Config.Progress != nil {
			mark := "完成"
			if err != nil {
				mark = "失败"
			}
			g.Config.Progress(mark + " " + name)
		}
		res.Steps = append(res.Steps, st)
		return err == nil
	}

	if !in.PostMerge {
		var diff DiffStat
		if !step("改动规模", func() (string, error) {
			d, err := diffStat(cctx, g.Config.Repo, in.BaseBranch, in.CandBranch)
			if err != nil {
				return "", err
			}
			diff = d
			if g.Config.MaxFiles > 0 && d.Files > g.Config.MaxFiles {
				return "", fmt.Errorf("改动文件 %d 个，超过上限 %d", d.Files, g.Config.MaxFiles)
			}
			if g.Config.MaxDiffLines > 0 && d.Lines() > g.Config.MaxDiffLines {
				return "", fmt.Errorf("改动 %d 行，超过上限 %d", d.Lines(), g.Config.MaxDiffLines)
			}
			return fmt.Sprintf("%d 个文件，+%d/-%d", d.Files, d.Added, d.Deleted), nil
		}) {
			res.Diff = diff
			res.OK = false
			return res, nil
		}
		res.Diff = diff

		if !step("受保护路径", func() (string, error) {
			return checkProtected(cctx, in.BaseDir, in.CandDir, g.protected())
		}) {
			res.OK = false
			return res, nil
		}

		if !step("测试完整性", func() (string, error) {
			return checkTestChanges(cctx, g.Config.Repo, in.BaseBranch, in.CandBranch)
		}) {
			res.OK = false
			return res, nil
		}

		if !step("gofmt", func() (string, error) {
			return checkFormat(cctx, g.Config.Repo, in.BaseBranch, in.CandBranch, in.CandDir, g.gofmtBin())
		}) {
			res.OK = false
			return res, nil
		}
	}

	if !step("go vet", func() (string, error) {
		out, err := runCmd(cctx, in.CandDir, 10*time.Minute, nil, g.goBin(), "vet", "-unsafeptr=false", "./...")
		if err != nil {
			return out.Stderr, err
		}
		return "通过", nil
	}) {
		res.OK = false
		return res, nil
	}

	if !step("go test", func() (string, error) {
		// 不加 -count=1：候选只改少量包，其余包直接吃 Go 测试缓存，
		// 否则一次全量测试在 Win7/杀毒环境下可能十几分钟。缓存键包含源码与 flags，
		// 没改过的包缓存命中不会掩盖候选引入的失败。
		// W7H_GO 让 internal/bench 的 fixture 测试也能找到同一个 go。
		out, err := runCmd(cctx, in.CandDir, g.testTimeout(), []string{"W7H_GO=" + g.goBin()}, g.goBin(),
			"test", "./...", "-timeout", g.testTimeout().String())
		if err != nil {
			return lastLines(out.Stdout+"\n"+out.Stderr, 30), err
		}
		return "通过", nil
	}) {
		res.OK = false
		return res, nil
	}

	if g.Config.BenchEnabled && !in.PostMerge {
		files, _ := changedFiles(cctx, g.Config.Repo, in.BaseBranch, in.CandBranch)
		if shouldBench(files) {
			if !step("agent bench", func() (string, error) {
				if _, err := os.Stat(filepath.Join(in.CandDir, "cmd", "bench", "main.go")); err != nil {
					return "候选里没有 cmd/bench，跳过", nil
				}
				task := strings.TrimSpace(g.Config.BenchTask)
				if task == "" {
					task = "argsfix"
				}
				timeout := g.Config.BenchTimeout
				if timeout <= 0 {
					timeout = 5 * time.Minute
				}
				out, err := runCmd(cctx, in.CandDir, timeout,
					[]string{"W7H_GO=" + g.goBin()}, g.goBin(),
					"run", "./cmd/bench", "-task", task, "-timeout", "3m")
				if err != nil {
					return lastLines(out.Stdout+"\n"+out.Stderr, 30), err
				}
				return "通过（" + task + "）", nil
			}) {
				res.OK = false
				return res, nil
			}
		}
	}

	for _, arch := range []string{"386", "amd64"} {
		arch := arch
		if !step("构建 "+arch, func() (string, error) {
			env := []string{"GOOS=windows", "GOARCH=" + arch, "CGO_ENABLED=0"}
			out, err := runCmd(cctx, in.CandDir, 15*time.Minute, env, g.goBin(), "build", "./...")
			if err != nil {
				return lastLines(out.Stdout+"\n"+out.Stderr, 30), err
			}
			return "通过", nil
		}) {
			res.OK = false
			return res, nil
		}
	}

	if !step("评测基线", func() (string, error) {
		if len(in.BaseEval.Results) == 0 {
			base, err := RunEval(cctx, in.BaseDir, g.goBin())
			if err != nil {
				return "", fmt.Errorf("基线评测失败：%w", err)
			}
			res.BaseEval = base
		}
		cur, err := RunEval(cctx, in.CandDir, g.goBin())
		if err != nil {
			return "", err
		}
		res.Eval = cur
		cmp := eval.Compare(res.BaseEval, cur)
		if !cmp.OK() {
			return cmpDetail(cmp), fmt.Errorf("评测退步：%s", cmpDetail(cmp))
		}
		return fmt.Sprintf("通过率 %.1f%% → %.1f%%，无退步", cmp.BaseScore*100, cmp.CurrentScore*100), nil
	}) {
		res.OK = false
		return res, nil
	}

	if !step("端到端自检", func() (string, error) {
		return RunSelftest(cctx, in.CandDir, g.goBin())
	}) {
		res.OK = false
		return res, nil
	}

	// 测试/构建不应该改动被提交的树；如果改了，说明候选的“通过”是在
	// 一个和提交内容不一致的工作区里得到的，不能合并。
	if !step("工作区清洁", func() (string, error) {
		st, err := statusPorcelain(cctx, in.CandDir)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(st) != "" {
			return st, fmt.Errorf("门禁运行后工作区不干净，拒绝合并：\n%s", clip(st, 1200))
		}
		return "干净", nil
	}) {
		res.OK = false
		return res, nil
	}

	res.OK = ok
	return res, nil
}

// shouldBench 判断候选是否改到了模型侧路径。只有这些改动才值得花模型 token
// 跑 agent bench；纯文档/测试/eval 改动不跑。
func shouldBench(files []string) bool {
	prefixes := []string{
		"internal/agent/",
		"internal/tools/",
		"internal/llm/",
		"internal/skills/",
		"internal/bench/",
		"cmd/bench/",
	}
	for _, f := range files {
		f = filepath.ToSlash(f)
		for _, p := range prefixes {
			if strings.HasPrefix(f, p) {
				return true
			}
		}
	}
	return false
}

// RunEval 用候选目录里的 evalrun 跑一遍 base 评测，返回结构化结果。
func RunEval(ctx context.Context, dir, goBin string) (eval.Report, error) {
	var zero eval.Report
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("harness-eval-rsi-%d", time.Now().UnixNano()))
	res, err := runCmd(ctx, dir, 10*time.Minute, nil, goBin, "run", "./cmd/evalrun", "-json", "-tmp", tmp)
	if err != nil {
		return zero, fmt.Errorf("evalrun 失败：%w", err)
	}
	var rep eval.Report
	if err := json.Unmarshal([]byte(res.Stdout), &rep); err != nil {
		return zero, fmt.Errorf("解析评测 JSON 失败：%w（stdout: %s）", err, clip(res.Stdout, 300))
	}
	return rep, nil
}

var selftestSummary = regexp.MustCompile(`PASS=(\d+)\s+FAIL=(\d+)\s+SKIP=(\d+)\s+TOTAL=(\d+)`)

// RunSelftest 跑一遍端到端自检。skip 不算失败，但 total=0 视为没跑起来。
func RunSelftest(ctx context.Context, dir, goBin string) (string, error) {
	sandbox, err := os.MkdirTemp("", "harness-selftest-rsi-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(sandbox)
	res, err := runCmd(ctx, dir, 20*time.Minute, nil, goBin, "run", "./cmd/selftest_run", sandbox)
	out := res.Stdout + "\n" + res.Stderr
	m := selftestSummary.FindStringSubmatch(out)
	if m == nil {
		if err != nil {
			return lastLines(out, 30), err
		}
		return lastLines(out, 30), fmt.Errorf("没有找到自检汇总行")
	}
	var pass, fail, total int
	fmt.Sscanf(m[1], "%d", &pass)
	fmt.Sscanf(m[2], "%d", &fail)
	fmt.Sscanf(m[4], "%d", &total)
	if total == 0 {
		return "", fmt.Errorf("自检没有执行任何用例")
	}
	if fail > 0 {
		return fmt.Sprintf("PASS=%d FAIL=%d TOTAL=%d", pass, fail, total),
			fmt.Errorf("自检失败 %d 项", fail)
	}
	return fmt.Sprintf("PASS=%d FAIL=%d TOTAL=%d", pass, fail, total), nil
}

// checkProtected 对比基线与候选的受保护文件哈希。
func checkProtected(ctx context.Context, baseDir, candDir string, paths []string) (string, error) {
	base, err := hashPaths(baseDir, paths)
	if err != nil {
		return "", err
	}
	cand, err := hashPaths(candDir, paths)
	if err != nil {
		return "", err
	}
	var changed []string
	for k, v := range base {
		if cand[k] != v {
			changed = append(changed, k)
		}
	}
	for k := range cand {
		if _, ok := base[k]; !ok {
			changed = append(changed, k)
		}
	}
	if len(changed) > 0 {
		return "", fmt.Errorf("候选修改了受保护路径：%s", strings.Join(changed, ", "))
	}
	return "未改动", nil
}

// checkTestChanges 允许候选新增测试行，但不允许删改既有测试。
// 这样“加一个失败后通过的回归测试”能过，而“删断言/删用例让门禁变绿”会被拦住。
func checkTestChanges(ctx context.Context, repo, baseBranch, candBranch string) (string, error) {
	out, err := gitOut(ctx, repo, "diff", "--numstat", baseBranch+"..."+candBranch, "--", "*_test.go")
	if err != nil {
		return "", err
	}
	var bad []string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		parts := strings.Split(ln, "\t")
		if len(parts) < 3 {
			continue
		}
		deleted := -1
		if parts[1] != "-" {
			if n, err := strconv.Atoi(parts[1]); err == nil {
				deleted = n
			}
		}
		if deleted != 0 {
			bad = append(bad, parts[2])
		}
	}
	if len(bad) > 0 {
		return "", fmt.Errorf("候选删改了既有测试（只允许新增测试行）：%s", strings.Join(bad, ", "))
	}
	return "未删改既有测试", nil
}

func hashPaths(root string, paths []string) (map[string]string, error) {
	out := make(map[string]string)
	for _, rel := range paths {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(rel))
		st, err := os.Stat(full)
		if err != nil {
			out[rel] = "<missing>"
			continue
		}
		if !st.IsDir() {
			h, err := hashFile(full)
			if err != nil {
				return nil, err
			}
			out[rel] = h
			continue
		}
		err = filepath.WalkDir(full, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			h, err := hashFile(p)
			if err != nil {
				return err
			}
			relPath, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(relPath)] = h
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// checkFormat 只检查候选改过的 Go 文件，避免历史未格式化文件把门禁永远卡红。
func checkFormat(ctx context.Context, repo, baseBranch, candBranch, candDir, gofmtBin string) (string, error) {
	files, err := changedFiles(ctx, repo, baseBranch, candBranch)
	if err != nil {
		return "", err
	}
	var goFiles []string
	for _, f := range files {
		if !strings.HasSuffix(strings.ToLower(f), ".go") {
			continue
		}
		if _, err := os.Stat(filepath.Join(candDir, filepath.FromSlash(f))); err != nil {
			continue // 删掉的文件不用检查格式
		}
		goFiles = append(goFiles, filepath.FromSlash(f))
	}
	if len(goFiles) == 0 {
		return "无 Go 文件变更", nil
	}
	args := append([]string{"-l"}, goFiles...)
	out, err := runCmd(ctx, candDir, 2*time.Minute, nil, gofmtBin, args...)
	if err != nil {
		return out.Stderr, err
	}
	if strings.TrimSpace(out.Stdout) != "" {
		return "", fmt.Errorf("以下文件未格式化：%s", strings.TrimSpace(out.Stdout))
	}
	return fmt.Sprintf("检查 %d 个文件", len(goFiles)), nil
}

func cmpDetail(c eval.Comparison) string {
	var parts []string
	if len(c.Regressions) > 0 {
		parts = append(parts, "退步："+strings.Join(c.Regressions, ", "))
	}
	if len(c.Missing) > 0 {
		parts = append(parts, "消失："+strings.Join(c.Missing, ", "))
	}
	if len(parts) == 0 {
		return "通过"
	}
	return strings.Join(parts, "；")
}

func lastLines(s string, n int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
