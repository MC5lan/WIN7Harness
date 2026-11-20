// Package selftest 提供端到端自检基线（v1.19 起引入）。
//
// 与 internal/core 里的 RunChecks 区别：
//   - RunChecks 是只读环境探测，回答"这台机器能不能跑"；
//   - selftest 是真实执行，真刀真枪调用工具集/快照/原子写，回答"跑起来对不对"。
//
// 设计目标是"真机验证闭环"：用户在任何一台 Win7/Win10/Win11 上执行
//
//	win7harness.exe -selftest
//
// 就能得到一份带通过率、耗时、失败细节的报告文件，直接回传给开发者定位，
// 不需要远程连机器、不需要装任何运行库。
package selftest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"win7harness/internal/core"
)

// Status 单条用例结果。
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

// Case 一条用例的执行结果。
type Case struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Group  string `json:"group"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
	DurMS  int64  `json:"dur_ms"`
}

// Report 一次完整自检的汇总。
type Report struct {
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	WorkDir string `json:"work_dir"`
	DataDir string `json:"data_dir"`
	Started string `json:"started"`
	DurMS   int64  `json:"dur_ms"`
	Cases   []Case `json:"cases"`

	// Err 运行前的准备失败（例如临时目录建不出来），非空时 Cases 可能不完整。
	Err string `json:"err,omitempty"`
}

// ErrNotConfigured 表示模型尚未配置，连通性用例按跳过处理而不是失败。
var ErrNotConfigured = errors.New("未配置模型（缺接口地址 / API Key / 模型名）")

// Options 自检参数。
type Options struct {
	// WorkDir 沙箱工作目录，用例只在这里读写。为空时自动在数据目录下新建。
	WorkDir string
	// DataDir 数据目录，默认 core.DataDir()。
	DataDir string
	// Timeout 单条用例的超时，默认 30s。
	Timeout time.Duration
	// KeepSandbox 保留自检沙箱目录，失败取证用。
	KeepSandbox bool
	// ProviderTest 可选：真实发一次模型请求的探测函数，由调用方注入（GUI/CLI 各自实现，
	// 避免 selftest 包直接依赖网络层配置细节）。返回错误表示不可用。
	ProviderTest func() error
}

// 用例定义（内部用）。
type testCase struct {
	id    string
	name  string
	group string
	run   func(*env) (Status, string, string)
}

// env 用例可用的沙箱环境。
type env struct {
	workDir string
	dataDir string
	opt     Options
	// anyTimedOut 置位后，Run 收尾不删除沙箱：超时用例的后台 goroutine 可能还在写，
	// 删掉目录 = 与活着的协程抢写同一批文件（Windows 直接报错/偶发把留下的一半当有效产物）。
	anyTimedOut bool
}

func (e *env) path(name string) string { return filepath.Join(e.workDir, name) }

// Run 执行全部用例并返回报告。
//
// 数据目录强制隔离到沙箱：把 WIN7HARNESS_HOME 重定向到 opt.DataDir（缺省自动创建
// 临时目录并在结束时清理），期间 core 的所有读写（快照/日志/索引/配置/设备表）
// 都不会碰真实用户数据目录。调用方需要"真实配置"做探测时，必须在调用 Run 之前
// 取好（例如 diag.go 提前加载 cfg 再闭包引用）——WIN7HARNESS_HOME 一设，
// core.Load() 读到的就是沙箱里的默认配置。
func Run(opt Options) *Report {
	if opt.Timeout <= 0 {
		opt.Timeout = 30 * time.Second
	}
	// 先准备沙箱数据目录
	ownDataDir := false
	if strings.TrimSpace(opt.DataDir) == "" {
		tmp, err := os.MkdirTemp("", "win7harness-selftest-data-*")
		if err != nil {
			return &Report{Version: core.Version, OS: osName(), Arch: runtime.GOARCH, Err: "无法创建自检数据目录：" + err.Error()}
		}
		opt.DataDir = tmp
		ownDataDir = true
	} else if err := os.MkdirAll(opt.DataDir, 0o755); err != nil {
		return &Report{Version: core.Version, OS: osName(), Arch: runtime.GOARCH, Err: "自检数据目录不可写：" + err.Error()}
	}
	oldHome, hadHome := os.LookupEnv("WIN7HARNESS_HOME")
	_ = os.Setenv("WIN7HARNESS_HOME", opt.DataDir)
	defer func() {
		if ownDataDir && !opt.KeepSandbox {
			_ = os.RemoveAll(opt.DataDir)
		}
		if hadHome {
			_ = os.Setenv("WIN7HARNESS_HOME", oldHome)
		} else {
			_ = os.Unsetenv("WIN7HARNESS_HOME")
		}
		core.SetCurrentConv("")
	}()
	core.SetCurrentConv("")
	rep := &Report{
		Version: core.Version,
		OS:      osName(),
		Arch:    runtime.GOARCH,
		DataDir: opt.DataDir,
		Started: time.Now().Format("2006-01-02 15:04:05"),
	}
	start := time.Now()

	workDir, cleanup, err := prepareWorkDir(opt)
	if err != nil {
		rep.Err = err.Error()
		rep.DurMS = time.Since(start).Milliseconds()
		return rep
	}
	e := &env{workDir: workDir, dataDir: opt.DataDir, opt: opt}
	defer func() {
		if opt.KeepSandbox {
			return
		}
		if e.anyTimedOut {
			core.Logf("自检：有用例超时，保留沙箱 %s 用于排查", workDir)
			return
		}
		cleanup()
	}()
	rep.WorkDir = workDir

	for _, tc := range allCases() {
		rep.Cases = append(rep.Cases, runOne(e, tc))
	}
	rep.DurMS = time.Since(start).Milliseconds()
	return rep
}

// runOne 单条用例执行，带 panic 兜底与超时。
// 每个用例拿到自己的子目录（workDir/<ID>）——上一个用例落盘的残留
// 文件（watch.json、schedule.json 等）绝不会被下一个用例读到；
// 超时或崩溃的用例也不会污染其它用例。
func runOne(e *env, tc testCase) (c Case) {
	c = Case{ID: tc.id, Name: tc.name, Group: tc.group, Status: StatusFail}
	start := time.Now()

	// 每用例独立沙箱子目录（注：用例并发安全——env.path 落各自子目录）
	caseDir := filepath.Join(e.workDir, tc.id)
	_ = os.RemoveAll(caseDir)
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		c.Detail = "无法创建用例目录：" + err.Error()
		c.DurMS = time.Since(start).Milliseconds()
		return c
	}
	caseEnv := &env{workDir: caseDir, dataDir: e.dataDir, opt: e.opt}
	timedOut := false
	defer func() {
		if !e.opt.KeepSandbox && !timedOut {
			_ = os.RemoveAll(caseDir)
		}
	}()
	defer func() {
		if r := recover(); r != nil {
			c.Status = StatusFail
			c.Detail = fmt.Sprintf("用例崩溃：%v", r)
			c.Hint = "这是缺陷，请把报告回传给开发者"
		}
		c.DurMS = time.Since(start).Milliseconds()
	}()

	type outcome struct {
		st     Status
		detail string
		hint   string
	}
	ch := make(chan outcome, 1)
	core.SafeGo("selftest/suite.go:156", func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- outcome{StatusFail, fmt.Sprintf("用例崩溃：%v", r), "这是缺陷，请把报告回传给开发者"}
			}
		}()
		st, detail, hint := tc.run(caseEnv)
		ch <- outcome{st, detail, hint}
	})

	select {
	case o := <-ch:
		c.Status, c.Detail, c.Hint = o.st, o.detail, o.hint
	case <-time.After(e.opt.Timeout):
		timedOut = true
		e.anyTimedOut = true
		c.Status = StatusFail
		c.Detail = fmt.Sprintf("超时（>%s）· 用例的后台 goroutine 可能仍在运行，已保留其目录用于排查", e.opt.Timeout)
		c.Hint = "该环境执行过慢或卡死，检查杀毒软件/磁盘状态"
	}
	return c
}

// prepareWorkDir 建沙箱目录；调用方给的目录不删除，自建的才清理。
func prepareWorkDir(opt Options) (string, func(), error) {
	if opt.WorkDir != "" {
		if err := os.MkdirAll(opt.WorkDir, 0o755); err != nil {
			return "", func() {}, fmt.Errorf("工作目录不可用：%v", err)
		}
		return opt.WorkDir, func() {}, nil
	}
	base := filepath.Join(opt.DataDir, "selftest")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", func() {}, fmt.Errorf("无法创建自检目录 %s：%v", base, err)
	}
	// 沙箱目录必须唯一。过去只用"到秒"的时间戳，而 UI 里点一次自检会走到两次
	// （进诊断页自动跑一次 + 点按钮再跑一次），两次落进同一个目录，上一轮落盘的
	// watch.json / sched.json / devices.json 被下一轮读到，于是出现"写入 2 条读回 4 条"
	// "新增文件为空""清理误伤有效设备"这类**假失败**——自检最不该骗人。
	// 这里用 os.Mkdir 而非 MkdirAll：创建是原子的，撞名就换一个，绝不会复用旧目录。
	dir := ""
	for i := 0; i < 16; i++ {
		now := time.Now()
		cand := filepath.Join(base, fmt.Sprintf("run-%s-%06d", now.Format("20060102-150405"), now.Nanosecond()/1000))
		if err := os.Mkdir(cand, 0o755); err == nil {
			dir = cand
			break
		} else if !os.IsExist(err) {
			return "", func() {}, fmt.Errorf("无法创建自检沙箱 %s：%v", cand, err)
		}
	}
	if dir == "" {
		return "", func() {}, fmt.Errorf("无法创建自检沙箱（目录名连续撞车）：%s", base)
	}
	return dir, func() {
		// 只清理自己建的沙箱，且限定在 selftest 目录下，避免误删。
		if strings.HasPrefix(filepath.Clean(dir), filepath.Clean(base)+string(os.PathSeparator)) {
			os.RemoveAll(dir)
		}
	}, nil
}

// ---------- 统计 ----------

// Count 指定状态的数量。
func (r *Report) Count(s Status) int {
	n := 0
	for _, c := range r.Cases {
		if c.Status == s {
			n++
		}
	}
	return n
}

// Total 用例总数。
func (r *Report) Total() int { return len(r.Cases) }

// PassRate 通过率（跳过的不计入分母）。没有可执行用例时返回 0。
func (r *Report) PassRate() float64 {
	ran := r.Total() - r.Count(StatusSkip)
	if ran <= 0 {
		return 0
	}
	return float64(r.Count(StatusPass)) / float64(ran) * 100
}

// OK 是否全部通过（允许 skip，不允许 fail）。
func (r *Report) OK() bool { return r.Err == "" && r.Count(StatusFail) == 0 && r.Total() > 0 }

// ExitCode 供命令行使用：0 全绿，1 有失败，2 准备失败。
func (r *Report) ExitCode() int {
	if r.Err != "" {
		return 2
	}
	if r.OK() {
		return 0
	}
	return 1
}

// ---------- 输出 ----------

// Text 控制台输出。宽度按老机器 80 列终端友好排版，不用颜色与特殊字符。
func (r *Report) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "WIN7 Harness 自检 v%s\n", r.Version)
	fmt.Fprintf(&b, "系统：%s/%s   时间：%s\n", r.OS, r.Arch, r.Started)
	if r.WorkDir != "" {
		fmt.Fprintf(&b, "沙箱：%s\n", r.WorkDir)
	}
	b.WriteString(strings.Repeat("-", 64) + "\n")
	if r.Err != "" {
		fmt.Fprintf(&b, "自检未能开始：%s\n", r.Err)
		return b.String()
	}
	for _, c := range r.Cases {
		fmt.Fprintf(&b, "[%s] %-6s %s (%dms)\n", tag(c.Status), c.ID, c.Name, c.DurMS)
		if c.Status != StatusPass && c.Detail != "" {
			fmt.Fprintf(&b, "        %s\n", c.Detail)
		}
		if c.Status != StatusPass && c.Hint != "" {
			fmt.Fprintf(&b, "        建议：%s\n", c.Hint)
		}
	}
	b.WriteString(strings.Repeat("-", 64) + "\n")
	fmt.Fprintf(&b, "合计 %d 项：通过 %d，失败 %d，跳过 %d\n",
		r.Total(), r.Count(StatusPass), r.Count(StatusFail), r.Count(StatusSkip))
	fmt.Fprintf(&b, "通过率 %.1f%%（跳过不计入）   总耗时 %.2fs\n", r.PassRate(), float64(r.DurMS)/1000)
	if r.OK() {
		b.WriteString("结论：自检全部通过，本机环境可用。\n")
	} else {
		b.WriteString("结论：存在失败项，请按上面的建议处理后重跑，或把报告回传给开发者。\n")
	}
	return b.String()
}

func tag(s Status) string {
	switch s {
	case StatusPass:
		return "通过"
	case StatusSkip:
		return "跳过"
	default:
		return "失败"
	}
}

// Markdown 报告，用于落盘与反馈。
func (r *Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# WIN7 Harness 自检报告 v%s\n\n", r.Version)
	fmt.Fprintf(&b, "- 时间：%s\n- 系统：%s/%s\n- 版本：%s\n- 总耗时：%.2fs\n",
		r.Started, r.OS, r.Arch, r.Version, float64(r.DurMS)/1000)
	if r.WorkDir != "" {
		fmt.Fprintf(&b, "- 沙箱目录：`%s`\n", r.WorkDir)
	}
	b.WriteString("\n")
	if r.Err != "" {
		fmt.Fprintf(&b, "## 未能开始\n\n%s\n", r.Err)
		return b.String()
	}
	fmt.Fprintf(&b, "## 总览\n\n| 通过 | 失败 | 跳过 | 通过率 | 结论 |\n|---|---|---|---|---|\n| %d | %d | %d | %.1f%% | %s |\n\n",
		r.Count(StatusPass), r.Count(StatusFail), r.Count(StatusSkip), r.PassRate(), verdict(r))

	b.WriteString("## 明细\n\n| 编号 | 用例 | 分组 | 结果 | 耗时 |\n|---|---|---|---|---|\n")
	for _, c := range r.Cases {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %dms |\n", c.ID, c.Name, c.Group, tag(c.Status), c.DurMS)
	}
	b.WriteString("\n")

	var bad []Case
	for _, c := range r.Cases {
		if c.Status != StatusPass {
			bad = append(bad, c)
		}
	}
	if len(bad) == 0 {
		b.WriteString("## 异常项\n\n无。\n")
		return b.String()
	}
	b.WriteString("## 异常项\n\n")
	for _, c := range bad {
		fmt.Fprintf(&b, "### %s %s（%s）\n\n- 结果：%s\n- 详情：%s\n", c.ID, c.Name, c.Group, tag(c.Status), c.Detail)
		if c.Hint != "" {
			fmt.Fprintf(&b, "- 建议：%s\n", c.Hint)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func verdict(r *Report) string {
	if r.OK() {
		return "全部通过"
	}
	return "存在失败"
}

// JSON 机器可读输出。
func (r *Report) JSON() string {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"err":%q}`, err.Error())
	}
	return string(b)
}

// WriteReport 把报告写到 dir 下（Markdown + JSON 各一份），返回两个路径。
func (r *Report) WriteReport(dir string) (mdPath, jsonPath string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	stamp := time.Now().Format("20060102-150405")
	mdPath = filepath.Join(dir, "selftest-"+stamp+".md")
	jsonPath = filepath.Join(dir, "selftest-"+stamp+".json")
	if err := core.WriteFileAtomic(mdPath, []byte(r.Markdown()), 0o644); err != nil {
		return "", "", err
	}
	if err := core.WriteFileAtomic(jsonPath, []byte(r.JSON()), 0o644); err != nil {
		return mdPath, "", err
	}
	return mdPath, jsonPath, nil
}

// DefaultReportDir 默认报告目录（数据目录下 selftest/reports）。
func DefaultReportDir() string { return filepath.Join(core.LocalDataDir(), "selftest", "reports") }

// SortCases 按 ID 排序（外部若手工拼装报告可用）。
func (r *Report) SortCases() {
	sort.SliceStable(r.Cases, func(i, j int) bool { return r.Cases[i].ID < r.Cases[j].ID })
}

func osName() string {
	if runtime.GOOS == "windows" {
		return "Windows"
	}
	return runtime.GOOS
}
