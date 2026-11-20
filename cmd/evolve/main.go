// Command evolve 是 WIN7 Harness 自迭代（RSI-lite）的无界面入口。
//
// Stage 0 只做影子迭代：候选合入 rsi/auto，不动 main。
// 用法示例：
//
//	go run ./cmd/evolve -dry-run
//	go run ./cmd/evolve -max 1 -goal "补一组针对快照去重的回归测试"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"win7harness/internal/agent"
	"win7harness/internal/bootstrap"
	"win7harness/internal/core"
	"win7harness/internal/eval"
	"win7harness/internal/evolve"
)

func main() {
	repoFlag := flag.String("repo", "", "仓库目录（默认当前目录）")
	workRoot := flag.String("workroot", "", "worktree 根目录（默认仓库同级 win7harness-rsi）")
	baseBranch := flag.String("base", "", "基线分支（默认当前分支）")
	targetBranch := flag.String("target", "rsi/auto", "目标分支（Stage 0 影子分支）")
	maxCandidates := flag.Int("max", 2, "本轮到候选上限")
	maxRounds := flag.Int("rounds", 32, "单候选实现器工具轮数上限")
	maxTokens := flag.Int("max-tokens", 2000000, "本轮 token 上限（估算，按实现器用量累计）")
	maxMinutes := flag.Int("max-minutes", 180, "本轮墙钟上限（分钟）")
	maxDiff := flag.Int("max-diff", 600, "单候选增删行数上限")
	maxFiles := flag.Int("max-files", 12, "单候选改动文件数上限")
	gateTimeout := flag.Duration("gate-timeout", 40*time.Minute, "门禁总超时")
	testTimeout := flag.Duration("test-timeout", 20*time.Minute, "go test 超时")
	benchGate := flag.Bool("bench-gate", false, "候选改到 agent/tools/llm/skills 时跑一个 agent bench（消耗模型 token）")
	benchTask := flag.String("bench-task", "argsfix", "bench 门禁使用的 fixture")
	benchTimeout := flag.Duration("bench-timeout", 5*time.Minute, "bench 门禁超时")
	dryRun := flag.Bool("dry-run", false, "只生成提案，不执行、不合并")
	status := flag.Bool("status", false, "查看影子分支与实验历史（不需要模型）")
	nightly := flag.Bool("nightly", false, "常驻循环，每隔 -interval 跑一轮")
	interval := flag.Duration("interval", 24*time.Hour, "nightly 间隔")
	allowDirty := flag.Bool("allow-dirty", false, "允许主工作区有未提交改动（不建议）")
	goal := flag.String("goal", "", "本轮额外目标（可选）")
	proposalJSON := flag.String("proposal", "", "直接使用指定提案 JSON（跳过模型提案，便于复现）")
	proposalFile := flag.String("proposal-file", "", "从文件读取提案 JSON（跳过模型提案，便于复现）")
	patchFile := flag.String("patch", "", "直接把补丁应用到候选（跳过模型实现，便于复现）")
	modelFlag := flag.String("model", "", "覆盖模型（默认用配置里的模型）")
	goBin := flag.String("go", "go", "go 可执行文件")
	flag.Parse()

	// 自迭代门禁单独用一份 Go 构建缓存：开发机上的主会话/IDE 可能同时在跑
	// go test，共用默认 GOCACHE 会在锁上互相等，夜间门禁会被无关任务拖死。
	if os.Getenv("GOCACHE") == "" {
		_ = os.Setenv("GOCACHE", filepath.Join(os.TempDir(), "win7harness-rsi-gocache"))
	}

	repo := *repoFlag
	if repo == "" {
		repo, _ = os.Getwd()
	}
	repo, err := filepath.Abs(repo)
	if err != nil {
		fatal("解析仓库路径失败：%v", err)
	}
	if *workRoot == "" {
		*workRoot = filepath.Join(filepath.Dir(repo), "win7harness-rsi")
	}
	*workRoot, err = filepath.Abs(*workRoot)
	if err != nil {
		fatal("解析 worktree 路径失败：%v", err)
	}

	if *status {
		printStatus(repo, *baseBranch, *targetBranch)
		return
	}

	cfg := core.Load()
	model := strings.TrimSpace(*modelFlag)
	if model == "" {
		model = cfg.Model
	}
	p := cfg.Provider()
	needProposeModel := strings.TrimSpace(*proposalJSON) == "" && strings.TrimSpace(*proposalFile) == ""
	needImplementModel := !*dryRun && strings.TrimSpace(*patchFile) == ""
	if (needProposeModel || needImplementModel) && strings.TrimSpace(p.BaseURL) == "" {
		fatal("尚未配置模型服务地址，请先在设置里配置提供方（或用 -proposal + -patch 做离线复现）")
	}

	newEngine := func(workDir string, plan bool) (*agent.Engine, error) {
		eng, used, warn, err := bootstrap.NewEngineFor(cfg, p, model, workDir, bootstrap.Options{
			PlanMode: plan,
			// 无人值守：任何需要确认的操作一律拒绝。真正安全的命令走 allow 规则。
			Confirm: func(string, string) bool { return false },
			Emit: func(ev agent.Event) {
				if ev.Kind == "status" && ev.Text != "" && ev.Text != "思考中…" {
					fmt.Fprintln(os.Stderr, "  · "+ev.Text)
				}
			},
		})
		if err != nil {
			return nil, err
		}
		if warn != "" {
			fmt.Fprintln(os.Stderr, "! "+warn)
		}
		fmt.Fprintln(os.Stderr, "  模型："+used)
		// 两种模式都禁网、禁子代理：自迭代不应在无人值守时拉外部内容，
		// 也不应派生一个绕过路径限制的嵌套 agent。
		deny := []string{"web_fetch:*", "web_search:*", "task:*"}
		if plan {
			eng.Policy = core.NewPolicy(core.PermPlan, nil, deny, nil, nil)
		} else {
			// 实现器：工作区内文件可写，但越界写会触发确认并被 Confirm 拒绝。
			// 命令只放行 go/gofmt/git 只读；网络、子代理、git 写操作全部拒绝。
			eng.Policy = core.NewPolicy(core.PermAuto, []string{
				"run_command:go *",
				"run_command:gofmt *",
				"run_command:git diff *",
				"run_command:git status *",
				"run_command:git log *",
			}, append(deny,
				// apply_patch 在无人值守下会走确认闸门；实现器统一用 edit_file/write_file。
				"apply_patch:*",
				"run_command:cmd*", "run_command:powershell*", "run_command:pwsh*",
				"run_command:wscript*", "run_command:cscript*",
				"run_command:reg*", "run_command:sc*", "run_command:net*",
				"run_command:schtasks*", "run_command:taskkill*",
				"run_command:del*", "run_command:rd*", "run_command:rmdir*",
				"run_command:format*", "run_command:shutdown*",
				"run_command:curl*", "run_command:wget*",
				"run_command:git push*", "run_command:git commit*", "run_command:git checkout*",
				"run_command:git reset*", "run_command:git clean*", "run_command:git worktree*",
				"run_command:git branch*", "run_command:git tag*", "run_command:git config*",
				"run_command:git remote*", "run_command:git fetch*", "run_command:git pull*",
				// 只留 diff/status；翻历史对实现没帮助，还会把轮数烧光。
				"run_command:git log*", "run_command:git show*", "run_command:git grep*",
				"run_command:git rev-parse*", "run_command:git ls-files*", "run_command:git blame*",
				// 复合命令难审计，也让失败定位变模糊；拆成单条执行。
				"run_command:*&&*", "run_command:*||*",
				// 全量测试在实现器里非常慢（这个仓库 core 包就要十几分钟），
				// 交给外层门禁跑一次即可；实现器只跑目标包。
				"run_tests:*",
				"run_command:go test ./...*",
				"run_command:go test all*",
			), nil, nil)
			eng.VerifyMode = "off"
			eng.MaxToolRounds = *maxRounds
		}
		return eng, nil
	}

	propose := func(ctx context.Context, in evolve.ProposeInput) (evolve.ProposeResult, error) {
		raw := strings.TrimSpace(*proposalJSON)
		if raw == "" && strings.TrimSpace(*proposalFile) != "" {
			b, err := os.ReadFile(*proposalFile)
			if err != nil {
				return evolve.ProposeResult{}, fmt.Errorf("读取提案文件失败：%w", err)
			}
			raw = strings.TrimSpace(string(b))
		}
		if raw != "" {
			if strings.HasPrefix(raw, "{") {
				raw = "[" + raw + "]"
			}
			ps, err := evolve.ParseProposals(raw)
			return evolve.ProposeResult{Proposals: ps}, err
		}
		prompt := evolve.BuildProposePrompt(in)
		var lastRaw string
		for attempt := 0; attempt < 2; attempt++ {
			eng, err := newEngine(in.BaseDir, true)
			if err != nil {
				return evolve.ProposeResult{}, err
			}
			out, err := runSync(ctx, eng, prompt, 12*time.Minute)
			if err != nil {
				return evolve.ProposeResult{}, err
			}
			tin, tout, _ := eng.Usage()
			lastRaw = out
			ps, err := evolve.ParseProposals(out)
			if err == nil {
				return evolve.ProposeResult{Proposals: ps, Tokens: tin + tout}, nil
			}
			fmt.Fprintf(os.Stderr, "! 提案 JSON 解析失败（第 %d 次）：%v\n", attempt+1, err)
			prompt = evolve.BuildProposePrompt(in) +
				"\n\n上一次输出无法解析为合法 JSON：" + err.Error() +
				"\n请重新输出一个严格合法的 JSON 数组，不要重复上一次的格式错误。"
		}
		return evolve.ProposeResult{}, fmt.Errorf("提案 JSON 连续两次无法解析；最后一次原始输出：%s", clipText(lastRaw, 1200))
	}

	implement := func(ctx context.Context, in evolve.ImplementInput) (evolve.ImplementResult, error) {
		if strings.TrimSpace(*patchFile) != "" {
			if err := applyPatchFile(in.WorkDir, *patchFile); err != nil {
				return evolve.ImplementResult{}, err
			}
			return evolve.ImplementResult{Summary: "已应用指定补丁：" + *patchFile}, nil
		}
		eng, err := newEngine(in.WorkDir, false)
		if err != nil {
			return evolve.ImplementResult{}, err
		}
		out, err := runSync(ctx, eng, evolve.BuildImplementPrompt(in), time.Duration(*maxMinutes)*time.Minute)
		tin, tout, _ := eng.Usage()
		res := evolve.ImplementResult{Summary: out, Tokens: tin + tout}
		if err != nil && strings.Contains(err.Error(), "达到上限") {
			// 实现器经常在“已经改对”之后继续翻代码/查历史，把轮数烧光。
			// 只要工作区有真实改动，就交给门禁这个最终仲裁者；改坏了门禁会拦住。
			if has, herr := evolve.HasChanges(ctx, in.WorkDir); herr == nil && has {
				fmt.Fprintln(os.Stderr, "! 实现器轮数到顶，但工作区已有改动；交给门禁判定")
				res.Summary = "实现器轮数到顶，已有改动；由门禁判定"
				return res, nil
			}
		}
		return res, err
	}

	gate := evolve.DefaultGate{Config: evolve.GateConfig{
		Repo:         repo,
		GoBin:        *goBin,
		GofmtBin:     siblingGofmt(*goBin),
		Timeout:      *gateTimeout,
		TestTimeout:  *testTimeout,
		MaxDiffLines: *maxDiff,
		MaxFiles:     *maxFiles,
		BenchEnabled: *benchGate,
		BenchTask:    *benchTask,
		BenchTimeout: *benchTimeout,
		Progress: func(s string) {
			fmt.Println("[gate] " + s)
		},
	}}
	evalFn := func(ctx context.Context, dir string) (eval.Report, error) {
		return evolve.RunEval(ctx, dir, *goBin)
	}

	ecfg := evolve.Config{
		Repo:          repo,
		WorkRoot:      *workRoot,
		BaseBranch:    *baseBranch,
		TargetBranch:  *targetBranch,
		MaxCandidates: *maxCandidates,
		MaxRounds:     *maxRounds,
		MaxDiffLines:  *maxDiff,
		MaxFiles:      *maxFiles,
		MaxTokens:     *maxTokens,
		MaxWallClock:  time.Duration(*maxMinutes) * time.Minute,
		DryRun:        *dryRun,
		AllowDirty:    *allowDirty,
		Goal:          *goal,
		Propose:       propose,
		Implement:     implement,
		Gate:          gate.Run,
		Eval:          evalFn,
		Logf: func(format string, args ...interface{}) {
			fmt.Printf(format+"\n", args...)
		},
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	fails := 0
	for {
		if _, err := os.Stat(filepath.Join(repo, ".rsi", "STOP")); err == nil {
			fmt.Println("检测到 .rsi/STOP，退出夜间循环")
			return
		}
		rep, err := evolve.Run(ctx, ecfg)
		if err != nil {
			fails++
			fmt.Fprintf(os.Stderr, "\n自迭代失败：%v\n", err)
			if !*nightly || fails >= 3 {
				os.Exit(2)
			}
		} else {
			fails = 0
			fmt.Printf("\n结束：%s（%s）\n", rep.Stop, rep.StopDetail)
			fmt.Printf("报告：%s\n", filepath.Join(repo, "rsi-out", rep.ID, "run.md"))
		}
		if !*nightly {
			return
		}
		wait := *interval
		if wait <= 0 {
			wait = 24 * time.Hour
		}
		fmt.Printf("下一轮：%s 后（Ctrl+C 退出，或创建 .rsi/STOP）\n", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// runSync 把异步引擎包成同步调用：等到 turn_end，或者超时/取消时 Stop。
func runSync(ctx context.Context, eng *agent.Engine, prompt string, timeout time.Duration) (string, error) {
	done := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var content strings.Builder
	var lastErr string
	eng.Emit = func(ev agent.Event) {
		switch ev.Kind {
		case "content":
			mu.Lock()
			content.WriteString(ev.Text)
			mu.Unlock()
		case "error":
			mu.Lock()
			lastErr = ev.Text
			mu.Unlock()
		case "status":
			if ev.Text != "" && ev.Text != "思考中…" {
				fmt.Fprintln(os.Stderr, "  · "+ev.Text)
			}
		case "tool_start":
			fmt.Fprintln(os.Stderr, "  · 工具 "+ev.Name)
		case "tool_end":
			// 失败或编辑类工具的结果打出来：否则 apply_patch 被拒后模型
			// 只留下一个空 diff，外层完全看不出发生了什么。
			if ev.Err || ev.Name == "apply_patch" || ev.Name == "edit_file" {
				fmt.Fprintln(os.Stderr, "  · 结果 "+ev.Name+"："+clipText(ev.Text, 300))
			}
		case "turn_end":
			once.Do(func() { close(done) })
		}
	}
	eng.Send(prompt)
	// 引擎在“工具轮数到顶”“预算超限”等路径上会直接 return，不发 turn_end。
	// 只等 turn_end 会让无人值守进程一直挂到外层超时，所以同时盯 Running 状态。
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if !eng.Running() {
					once.Do(func() { close(done) })
					return
				}
			}
		}
	}()
	select {
	case <-done:
	case <-ctx.Done():
		eng.Stop()
		return "", ctx.Err()
	case <-time.After(timeout):
		eng.Stop()
		return "", fmt.Errorf("模型调用超时（%s）", timeout)
	}
	mu.Lock()
	errText := lastErr
	fallback := content.String()
	mu.Unlock()
	if errText != "" {
		return "", fmt.Errorf("%s", errText)
	}
	// content 事件包含工具调用前的中间话术；最终摘要取最后一条 assistant 正文。
	hist := eng.History()
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Role == "assistant" && strings.TrimSpace(hist[i].Content) != "" {
			return hist[i].Content, nil
		}
	}
	return fallback, nil
}

func applyPatchFile(dir, patch string) error {
	abs, err := filepath.Abs(patch)
	if err != nil {
		return err
	}
	cmd := exec.Command("git", "apply", "--whitespace=nowarn", abs)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply 失败：%v\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// printStatus 是观察期入口：不需要模型，只看影子分支与实验历史。
func printStatus(repo, base, target string) {
	if base == "" {
		base = gitText(repo, "branch", "--show-current")
		if base == "" {
			base = "master"
		}
	}
	fmt.Printf("仓库：%s\n基线：%s\n影子：%s\n\n", repo, base, target)
	if !gitBranchExists(repo, target) {
		fmt.Println("影子分支尚未创建；先跑一次 `evolve -max 1`。")
		return
	}
	ahead := gitText(repo, "rev-list", "--count", base+".."+target)
	behind := gitText(repo, "rev-list", "--count", target+".."+base)
	if ahead == "" {
		ahead = "0"
	}
	if behind == "" {
		behind = "0"
	}
	fmt.Printf("影子领先 %s：%s 个提交；落后：%s 个提交\n\n", base, ahead, behind)
	fmt.Println("最近提交：")
	if log := gitText(repo, "log", "--oneline", "-5", target); log != "" {
		fmt.Println(log)
	} else {
		fmt.Println("（暂无）")
	}

	hist := evolve.LoadHistory(repo, 100)
	fmt.Printf("\n实验记录：%d 条\n", len(hist))
	if len(hist) > 0 {
		counts := map[string]int{}
		for _, e := range hist {
			counts[e.Status]++
		}
		var keys []string
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("  %-18s %d\n", k, counts[k])
		}
		fmt.Println("\n最近实验：")
		start := 0
		if len(hist) > 5 {
			start = len(hist) - 5
		}
		for _, e := range hist[start:] {
			fmt.Printf("  %s  %-16s  %s\n", e.ID, e.Status, clipText(e.Title, 60))
		}
	}
}

func gitText(repo string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitBranchExists(repo, branch string) bool {
	return gitText(repo, "rev-parse", "--verify", "refs/heads/"+branch) != ""
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func siblingGofmt(goBin string) string {
	dir, name := filepath.Split(goBin)
	if strings.EqualFold(name, "go") || strings.EqualFold(name, "go.exe") {
		if strings.EqualFold(name, "go.exe") {
			return filepath.Join(dir, "gofmt.exe")
		}
		return filepath.Join(dir, "gofmt")
	}
	return "gofmt"
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
