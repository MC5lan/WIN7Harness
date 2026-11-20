// Package bench 用小型 Go fixture 衡量 agent harness 的真实修复能力：
// 每个 fixture 初始有一个失败测试，agent 在临时副本里修，修完再跑测试。
//
// 它不是工具链单测（internal/eval 的 base 层），而是“模型 + 工具 + 提示词”
// 的端到端任务集。自迭代要改 harness 时，这张成绩单比“9 个 base 用例全绿”
// 更有信号。
package bench

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed all:testdata
var fixtures embed.FS

// Task 是一个 fixture 任务。
type Task struct {
	ID     string
	Name   string
	Prompt string
	// Files 非空时用内存生成的 fixture（新增任务不必堆 testdata 目录）；
	// 为空时走 testdata/<ID> 的嵌入式 fixture。
	Files map[string]string
}

// Tasks 返回内置任务集。
func Tasks() []Task {
	common := "你在一个临时 Go 模块里修一个失败测试。先运行 `go test ./...` 看失败原因；" +
		"只改非测试源文件，不要修改任何 *_test.go、go.mod 或 go.sum；" +
		"改完再运行 `go test ./...` 确认通过。最后用一句话说明改了什么。"
	tasks := []Task{
		{
			ID:     "argsfix",
			Name:   "修复 --timeout= 等号形式",
			Prompt: common + "\n\n任务：让 `Parse` 支持 `--timeout=90` 这种等号形式，同时保持 `--timeout 90` 不变。",
		},
		{
			ID:     "slug",
			Name:   "修复 Slug 空白归一化",
			Prompt: common + "\n\n任务：让 `Slug` 正确去掉首尾空白、把连续空白折叠成一个连字符。",
		},
		{
			ID:     "dedupe",
			Name:   "修复切片去重",
			Prompt: common + "\n\n任务：让 `Unique` 真正去掉重复元素，并保留第一次出现的顺序。",
		},
		{
			ID:     "jsonfield",
			Name:   "修复 JSON 字段名",
			Prompt: common + "\n\n任务：让 `User.Email` 编码为 `email`，为空时仍按 `omitempty` 省略。",
		},
		{
			ID:   "multifile",
			Name: "跨文件修复配置归一化",
			Prompt: common + "\n\n任务：`client.New` 必须在构造时调用 `config.Config.Normalize`；" +
				"`Normalize` 对 `Retries <= 0` 回到 1，对 `TimeoutSec <= 0` 回到 30。两处都要改，不要改测试。",
		},
	}
	return append(tasks, generatedTasks()...)
}

// AgentFunc 在临时目录里执行一次修复任务，返回摘要与 token 用量。
type AgentFunc func(ctx context.Context, dir, prompt string) (summary string, tokens int, err error)

// Result 是单个任务的成绩。
type Result struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"` // pass / fail
	Detail     string `json:"detail,omitempty"`
	Ms         int64  `json:"ms"`
	Tokens     int    `json:"tokens,omitempty"`
	BeforeFail bool   `json:"before_fail"`
	AfterPass  bool   `json:"after_pass"`
}

// Report 是一次 bench 的汇总。
type Report struct {
	At      string   `json:"at"`
	Results []Result `json:"results"`
	Pass    int      `json:"pass"`
	Fail    int      `json:"fail"`
	TotalMs int64    `json:"total_ms"`
}

// RunAll 依次跑任务。每个任务都在独立的临时目录里，互不影响。
func RunAll(ctx context.Context, tasks []Task, agent AgentFunc, timeout time.Duration) Report {
	rep := Report{At: time.Now().Format("2006-01-02 15:04:05")}
	start := time.Now()
	for _, task := range tasks {
		res := runOne(ctx, task, agent, timeout)
		rep.Results = append(rep.Results, res)
		if res.Status == "pass" {
			rep.Pass++
		} else {
			rep.Fail++
		}
	}
	rep.TotalMs = time.Since(start).Milliseconds()
	return rep
}

func runOne(ctx context.Context, task Task, agent AgentFunc, timeout time.Duration) (res Result) {
	res = Result{ID: task.ID, Name: task.Name, Status: "fail"}
	t0 := time.Now()
	defer func() { res.Ms = time.Since(t0).Milliseconds() }()
	if agent == nil {
		res.Detail = "没有注入 agent"
		return res
	}
	dir, err := os.MkdirTemp("", "w7h-bench-"+task.ID+"-")
	if err != nil {
		res.Detail = "创建临时目录失败：" + err.Error()
		return res
	}
	defer os.RemoveAll(dir)
	if len(task.Files) > 0 {
		if err := writeTaskFiles(dir, task.Files); err != nil {
			res.Detail = "生成 fixture 失败：" + err.Error()
			return res
		}
	} else if err := copyFixture(task.ID, dir); err != nil {
		res.Detail = "复制 fixture 失败：" + err.Error()
		return res
	}
	before, err := protectedHashes(dir)
	if err != nil {
		res.Detail = "读取 fixture 哈希失败：" + err.Error()
		return res
	}
	pre, err := runGoTest(ctx, dir, timeout)
	if err != nil {
		res.Detail = "初始测试无法运行：" + err.Error()
		return res
	}
	if pre.Code == 0 {
		res.Detail = "fixture 初始状态没有失败，任务无效"
		return res
	}
	res.BeforeFail = true
	summary, tokens, err := agent(ctx, dir, task.Prompt)
	res.Tokens = tokens
	if err != nil {
		res.Detail = "agent 失败：" + err.Error()
		return res
	}
	after, err := protectedHashes(dir)
	if err != nil {
		res.Detail = "读取修复后哈希失败：" + err.Error()
		return res
	}
	if !sameHashes(before, after) {
		res.Detail = "agent 修改了受保护的测试/模块文件"
		return res
	}
	post, err := runGoTest(ctx, dir, timeout)
	if err != nil {
		res.Detail = "修复后测试无法运行：" + err.Error()
		return res
	}
	if post.Code != 0 {
		res.Detail = "修复后测试仍失败：\n" + lastLines(post.Output, 30)
		return res
	}
	res.AfterPass = true
	res.Status = "pass"
	res.Detail = strings.TrimSpace(summary)
	return res
}

func writeTaskFiles(dir string, files map[string]string) error {
	for rel, body := range files {
		rel = filepath.FromSlash(rel)
		target := filepath.Join(dir, rel)
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(dir)+string(filepath.Separator)) {
			return fmt.Errorf("fixture 路径越界: %s", rel)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func copyFixture(id, dst string) error {
	root := "testdata/" + id
	return fs.WalkDir(fixtures, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.FromSlash(root), filepath.FromSlash(p))
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// fixture 的 go.mod 以 .tmpl 存放：go:embed 不会把嵌套 module 目录
		// 当成可嵌入文件，复制到临时目录时再还原成 go.mod。
		if rel == "go.mod.tmpl" {
			rel = "go.mod"
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := fixtures.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// goCommand 允许门禁把绝对路径的 go 传进来：开发机上 go 不一定在 PATH 里。
func goCommand() string {
	if v := strings.TrimSpace(os.Getenv("W7H_GO")); v != "" {
		return v
	}
	return "go"
}

type testRun struct {
	Code   int
	Output string
}

func runGoTest(ctx context.Context, dir string, timeout time.Duration) (testRun, error) {
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, goCommand(), "test", "./...", "-count=1")
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	res := testRun{Output: string(out)}
	if err == nil {
		return res, nil
	}
	if ee, ok := err.(*exec.ExitError); ok {
		res.Code = ee.ExitCode()
		if cctx.Err() != nil {
			return res, fmt.Errorf("超时（%s）", timeout)
		}
		return res, nil
	}
	return res, err
}

func protectedHashes(dir string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, "_test.go") && name != "go.mod" && name != "go.sum" {
			return nil
		}
		h, err := hashFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = h
		return nil
	})
	return out, err
}

func hashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func sameHashes(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Markdown 生成人类可读报告。
func (r Report) Markdown() string {
	var b strings.Builder
	b.WriteString("# Agent Bench 报告\n\n")
	fmt.Fprintf(&b, "- 时间：%s\n- 通过：%d / %d\n- 总耗时：%.1fs\n\n", r.At, r.Pass, len(r.Results), float64(r.TotalMs)/1000)
	b.WriteString("| 任务 | 结果 | 耗时 | token | 说明 |\n| --- | --- | --- | --- | --- |\n")
	for _, x := range r.Results {
		fmt.Fprintf(&b, "| %s | %s | %.1fs | %d | %s |\n", x.ID, x.Status, float64(x.Ms)/1000, x.Tokens, oneLine(x.Detail))
	}
	return b.String()
}

// JSON 返回缩进 JSON，便于接看板。
func (r Report) JSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " / ")
	r := []rune(s)
	if len(r) > 160 {
		return string(r[:160]) + "…"
	}
	return s
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
