//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"win7harness/internal/agent"
	"win7harness/internal/bootstrap"
	"win7harness/internal/core"
	"win7harness/internal/win32"
)

// 命令行模式：把任务跑完就退出，方便脚本、批处理、CI 里调用。
//
//	win7harness.exe -run "把 src 下的 TODO 汇总成一个清单写到 todo.md"
//	win7harness.exe -run "..." --workdir D:\proj --yes
//	echo 任务描述 | win7harness.exe -run -
//	win7harness.exe -script 每日任务.txt --yes            # 串行批处理
//	win7harness.exe -script 每日任务.txt --keep-going     # 失败也继续往下跑
//
// 默认不放行任何写盘 / 执行命令的确认请求（无人值守时弹不出确认框），
// 需要真改动就显式加 --yes。

type runOptions = core.RunOptions

// taskResult 单个任务的执行结果。
type taskResult struct {
	OK        bool    `json:"ok"`
	No        int     `json:"no,omitempty"`
	Prompt    string  `json:"prompt,omitempty"`
	Line      int     `json:"line,omitempty"`
	Elapsed   float64 `json:"elapsed_sec"`
	TokensIn  int     `json:"tokens_in"`
	TokensOut int     `json:"tokens_out"`
	Answer    string  `json:"answer"`
	Error     string  `json:"error,omitempty"`
	Reasoning string  `json:"reasoning,omitempty"`
	TimedOut  bool    `json:"timed_out,omitempty"`
}

// runner 包一次命令行会话：引擎只装配一次，多个任务复用，
// 避免每条任务都重新读配置、连模型。
type runner struct {
	eng   *agent.Engine
	model string
	cfg   *core.Config
	opt   runOptions

	mu       sync.Mutex
	answer   strings.Builder
	reason   strings.Builder
	errText  string
	usageIn  int
	usageOut int
	finished chan struct{}
}

// runTask 执行一次任务，返回进程退出码。
// 0 成功；1 运行出错；2 参数或配置问题；3 超时。
func runTask(opt runOptions) int {
	if opt.Script != "" {
		return runScript(opt)
	}
	if strings.TrimSpace(opt.Prompt) == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "读取标准输入失败："+err.Error())
			return 2
		}
		opt.Prompt = string(data)
	}
	opt.Prompt = strings.TrimSpace(opt.Prompt)
	if opt.Prompt == "" {
		fmt.Fprintln(os.Stderr, "缺少任务内容。用法：win7harness.exe -run \"你的任务\"")
		return 2
	}

	r, code := newRunner(opt)
	if r == nil {
		return code
	}

	res := r.run(opt.Prompt, 0, 0)
	cost := (float64(res.TokensIn)*2 + float64(res.TokensOut)*8) / 1e6 // 粗略估算，仅作参考
	if opt.Format == "json" {
		single := struct {
			OK      bool    `json:"ok"`
			Model   string  `json:"model"`
			Elapsed float64 `json:"elapsed_sec"`
			Tokens  struct {
				In  int `json:"in"`
				Out int `json:"out"`
			} `json:"tokens"`
			Answer    string `json:"answer"`
			Error     string `json:"error,omitempty"`
			Reasoning string `json:"reasoning,omitempty"`
		}{OK: res.OK, Model: r.model, Elapsed: res.Elapsed, Answer: res.Answer, Error: res.Error, Reasoning: res.Reasoning}
		single.Tokens.In, single.Tokens.Out = res.TokensIn, res.TokensOut
		b, _ := json.Marshal(single)
		fmt.Println(string(b))
	} else if opt.Quiet {
		fmt.Print(res.Answer)
	} else {
		fmt.Fprintf(os.Stderr, "\n— 用时 %.1fs，token %d/%d，参考费用 $%.4f\n",
			res.Elapsed, res.TokensIn, res.TokensOut, cost)
	}
	switch {
	case res.TimedOut:
		return 3
	case !res.OK:
		return 1
	}
	return 0
}

// runScript 按任务清单串行执行。-keep-going 决定单条失败后是否继续。
func runScript(opt runOptions) int {
	raw, err := os.ReadFile(opt.Script)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读取任务清单失败："+err.Error())
		return 2
	}
	text, err := core.DecodeScript(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}

	r, code := newRunner(opt)
	if r == nil {
		return code
	}

	// 占位符取当前时间与工作目录，让同一份清单每天复用。
	// 工作目录要等 runner 建好才知道（-dir 未给时是配置或程序目录），
	// 否则 {{dir}} 会被替换成空串，任务里就成了 "清理 \temp"。
	now := time.Now()
	vars := core.ScriptVars(r.cfg.WorkDir, now.Format("2006-01-02"), now.Format("15:04"))
	tasks, err := core.ParseScript(text, vars)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}
	if len(tasks) == 0 {
		fmt.Fprintln(os.Stderr, "任务清单里没有任务（空行与 # 开头的行会被跳过）："+opt.Script)
		return 2
	}

	results := make([]taskResult, 0, len(tasks))
	statuses := make([]core.TaskStatus, 0, len(tasks))
	begin := time.Now()
	for _, t := range tasks {
		if opt.Format == "text" && !opt.Quiet {
			fmt.Fprintf(os.Stderr, "\n[%d/%d] %s\n", t.No, len(tasks), shorten(t.Text, 160))
		} else if opt.Format == "text" && opt.Quiet && t.No > 1 {
			fmt.Println()
		}
		res := r.run(t.Text, t.No, t.Line)
		results = append(results, res)
		statuses = append(statuses, core.TaskStatus{OK: res.OK, TimedOut: res.TimedOut})
		if res.TimedOut {
			break // 超时说明模型侧卡住，后面的任务大概率同样超时
		}
		if !res.OK && !opt.KeepGoing {
			fmt.Fprintf(os.Stderr, "\n第 %d 条任务失败，已停止（加 --keep-going 可继续跑后面的任务）\n", t.No)
			break
		}
	}

	okCount, failCount, skipped, code := core.SummarizeScript(len(tasks), statuses)
	total := struct {
		OK      bool         `json:"ok"`
		Tasks   []taskResult `json:"tasks"`
		Summary struct {
			Total     int     `json:"total"`
			Succeeded int     `json:"succeeded"`
			Failed    int     `json:"failed"`
			Skipped   int     `json:"skipped"`
			Elapsed   float64 `json:"elapsed_sec"`
			TokensIn  int     `json:"tokens_in"`
			TokensOut int     `json:"tokens_out"`
		} `json:"summary"`
	}{Tasks: results}
	total.Summary.Total, total.Summary.Succeeded = len(tasks), okCount
	total.Summary.Failed, total.Summary.Skipped = failCount, skipped
	total.Summary.Elapsed = time.Since(begin).Seconds()
	for _, res := range results {
		total.Summary.TokensIn += res.TokensIn
		total.Summary.TokensOut += res.TokensOut
	}
	total.OK = failCount == 0 && skipped == 0

	if opt.Format == "json" {
		b, _ := json.Marshal(total)
		fmt.Println(string(b))
	} else if !opt.Quiet {
		fmt.Fprintf(os.Stderr, "\n— 共 %d 条：成功 %d，失败 %d，跳过 %d；用时 %.1fs，token %d/%d\n",
			total.Summary.Total, okCount, failCount, skipped, total.Summary.Elapsed,
			total.Summary.TokensIn, total.Summary.TokensOut)
	}

	return code
}

// newRunner 装配引擎。返回 nil 时第二个值是退出码。
func newRunner(opt runOptions) (*runner, int) {
	cfg := core.Load()
	if opt.WorkDir != "" {
		cfg.WorkDir = opt.WorkDir
	}
	if opt.Timeout > 0 {
		cfg.TimeoutSec = opt.Timeout
	}
	if strings.TrimSpace(cfg.WorkDir) == "" {
		if wd, err := os.Getwd(); err == nil {
			cfg.WorkDir = wd
		}
	}

	r := &runner{cfg: cfg, opt: opt, finished: make(chan struct{}, 1)}
	confirm := func(title, detail string) bool {
		if !opt.Yes {
			fmt.Fprintf(os.Stderr, "[拒绝] 需要确认的操作（加 --yes 放行）：%s\n", title)
			return false
		}
		// --yes 只代表“工作区内的无人值守”，不能替用户批准越界读写。
		// 防止模型为了找 go/gofmt 往 %USERPROFILE%\.local\bin 写包装脚本。
		if strings.Contains(title, "工作区之外") || strings.Contains(detail, "工作区之外") {
			fmt.Fprintf(os.Stderr, "[拒绝] --yes 不批准工作区之外的路径操作：%s\n", title)
			return false
		}
		fmt.Fprintf(os.Stderr, "[放行] %s\n", title)
		return true
	}

	eng, model, warn, err := bootstrap.NewEngine(cfg, bootstrap.Options{
		Confirm:  confirm,
		PlanMode: opt.Plan,
		// 回调跑在引擎 goroutine 上，与主线程共享状态，统一在 r 内加锁。
		Emit: r.onEvent,
	})
	if warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return nil, 2
	}
	r.eng, r.model = eng, model
	return r, 0
}

// onEvent 收集单任务输出，同时负责把 text 模式的进度打到控制台。
func (r *runner) onEvent(ev agent.Event) {
	switch ev.Kind {
	case "content":
		r.mu.Lock()
		r.answer.WriteString(ev.Text)
		r.mu.Unlock()
		if r.opt.Format == "text" && !r.opt.Quiet {
			fmt.Print(ev.Text)
		}
	case "reasoning":
		r.mu.Lock()
		r.reason.WriteString(ev.Text)
		r.mu.Unlock()
	case "tool_start":
		if r.opt.Format == "text" && !r.opt.Quiet {
			fmt.Fprintf(os.Stderr, "\n· %s %s\n", ev.Name, shorten(ev.Args, 120))
		}
	case "tool_end":
		if r.opt.Format == "text" && !r.opt.Quiet {
			st := "ok"
			if ev.Err {
				st = "失败"
			}
			if ev.Dur > 0 {
				fmt.Fprintf(os.Stderr, "  ↳ %s（%.2fs）\n", st, ev.Dur.Seconds())
			} else {
				fmt.Fprintf(os.Stderr, "  ↳ %s\n", st)
			}
		}
	case "turn_end":
		r.mu.Lock()
		r.usageIn += ev.Usage.PromptTokens
		r.usageOut += ev.Usage.CompletionTokens
		r.mu.Unlock()
		r.signal()
	case "error":
		r.mu.Lock()
		r.errText = ev.Text
		r.mu.Unlock()
		fmt.Fprintln(os.Stderr, "错误："+ev.Text)
		r.signal()
	}
}

func (r *runner) signal() {
	select {
	case r.finished <- struct{}{}:
	default:
	}
}

// run 跑一条任务并等它真正结束。
func (r *runner) run(prompt string, no, line int) taskResult {
	r.mu.Lock()
	r.answer.Reset()
	r.reason.Reset()
	r.errText = ""
	r.usageIn, r.usageOut = 0, 0
	r.mu.Unlock()
	// 清掉上一条任务遗留的完成信号，否则短任务可能被误判为已完成
	select {
	case <-r.finished:
	default:
	}

	// 批处理默认任务之间互相隔离，--shared 才让后一条沿用前一条的上下文
	if !r.opt.Shared {
		r.eng.Reset()
	}

	started := time.Now()
	r.eng.Send(prompt)

	idle := time.Duration(r.cfg.TimeoutSec) * time.Second
	if idle < 30*time.Second {
		idle = 30 * time.Second
	}
	// 等引擎真正停下来：子 Agent 也会发 turn_end，只看事件会误判提前结束。
	// turn_end 是在 loop 返回前发出的，此时 running 还没清；必须再等引擎收尾，
	// 否则会一直等到 deadline（实测技能加载类任务会白等 150 秒）。
	deadline := time.NewTimer(idle + 30*time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	timedOut := false
	for {
		select {
		case <-r.finished:
		case <-ticker.C:
		case <-deadline.C:
			r.eng.Stop()
			timedOut = true
			fmt.Fprintln(os.Stderr, "任务超时，已中断。")
		}
		if !r.eng.Running() {
			break
		}
	}

	r.mu.Lock()
	res := taskResult{No: no, Line: line, Prompt: prompt, Elapsed: time.Since(started).Seconds(),
		Answer: r.answer.String(), Error: r.errText, Reasoning: r.reason.String(),
		TokensIn: r.usageIn, TokensOut: r.usageOut, TimedOut: timedOut}
	r.mu.Unlock()
	res.OK = res.Error == "" && !timedOut
	return res
}

func shorten(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// setupRunConsole 在图形界面启动前把 stdout 准备好。
func setupRunConsole() func() {
	return win32.SetupConsole("Win7 Harness")
}
