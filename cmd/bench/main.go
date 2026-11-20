// Command bench 用内置 fixture 跑 agent 端到端修复任务，衡量 harness 的真实能力。
// 这是自迭代的“模型层”尺子：base eval 只测工具链，bench 测模型 + 工具 + 提示词。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"win7harness/internal/agent"
	"win7harness/internal/bench"
	"win7harness/internal/bootstrap"
	"win7harness/internal/core"
)

func main() {
	taskFlag := flag.String("task", "", "只跑指定任务 ID（默认全部）")
	timeout := flag.Duration("timeout", 10*time.Minute, "单任务超时")
	outDir := flag.String("out", "bench-out", "报告输出目录")
	modelFlag := flag.String("model", "", "覆盖模型")
	goBin := flag.String("go", "go", "go 可执行文件")
	flag.Parse()
	if strings.TrimSpace(*goBin) != "" {
		_ = os.Setenv("W7H_GO", *goBin)
	}

	cfg := core.Load()
	model := strings.TrimSpace(*modelFlag)
	if model == "" {
		model = cfg.Model
	}
	p := cfg.Provider()
	if strings.TrimSpace(p.BaseURL) == "" {
		fatal("尚未配置模型服务地址，请先在设置里配置提供方")
	}

	agentFn := func(ctx context.Context, dir, prompt string) (string, int, error) {
		eng, _, warn, err := bootstrap.NewEngineFor(cfg, p, model, dir, bootstrap.Options{
			// bench 只测“模型 + 工具 + 提示词”，不测审批交互：全部自动接受。
			Confirm: func(string, string) bool { return true },
			ConfirmDiff: func(_ string, _ string, hunks []core.DiffHunk, _ bool) []int {
				all := make([]int, len(hunks))
				for i := range all {
					all[i] = i
				}
				return all
			},
			Emit: func(ev agent.Event) {
				if ev.Kind == "status" && ev.Text != "" && ev.Text != "思考中…" {
					fmt.Fprintln(os.Stderr, "  · "+ev.Text)
				}
			},
		})
		if err != nil {
			return "", 0, err
		}
		if warn != "" {
			fmt.Fprintln(os.Stderr, "! "+warn)
		}
		eng.Policy = core.NewPolicy(core.PermAuto,
			[]string{"run_command:go *", "run_command:gofmt *", "run_command:git *",
				"run_command:dir *", "run_command:type *", "run_command:where *", "run_command:findstr *"},
			[]string{"web_fetch:*", "web_search:*", "task:*"},
			nil, nil)
		eng.VerifyMode = "off"
		eng.MaxToolRounds = 24
		out, err := runSync(ctx, eng, prompt, *timeout)
		tin, tout, _ := eng.Usage()
		return out, tin + tout, err
	}

	tasks := bench.Tasks()
	if id := strings.TrimSpace(*taskFlag); id != "" {
		var one []bench.Task
		for _, t := range tasks {
			if t.ID == id {
				one = append(one, t)
			}
		}
		if len(one) == 0 {
			fatal("找不到任务 %s", id)
		}
		tasks = one
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rep := bench.RunAll(ctx, tasks, agentFn, *timeout)
	fmt.Println(rep.Markdown())
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal("创建报告目录失败：%v", err)
	}
	base := filepath.Join(*outDir, time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(base, 0o755); err != nil {
		fatal("创建报告目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "report.md"), []byte(rep.Markdown()), 0o644); err != nil {
		fatal("写报告失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "report.json"), []byte(rep.JSON()), 0o644); err != nil {
		fatal("写 JSON 失败：%v", err)
	}
	fmt.Println("报告：", base)
	if rep.Fail > 0 {
		os.Exit(1)
	}
}

// runSync 与 cmd/evolve 的同步包装同构：等 turn_end，或引擎停止/超时。
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
			if ev.Err || ev.Name == "apply_patch" || ev.Name == "edit_file" {
				fmt.Fprintln(os.Stderr, "  · 结果 "+ev.Name+"："+clipText(ev.Text, 300))
			}
		case "turn_end":
			once.Do(func() { close(done) })
		}
	}
	eng.Send(prompt)
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
		return "", fmt.Errorf("任务超时（%s）", timeout)
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

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
