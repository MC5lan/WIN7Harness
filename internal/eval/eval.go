// Package eval 提供可重复执行的评测用例（eval 基线）。
//
// 目的：把"这版到底行不行"从体感变成数字。用例分两层：
//   - base：不依赖模型，直接压工具链与索引、技能等底座能力，任何机器都能跑；
//   - model：依赖真实模型完成的端到端任务，没有配置 API Key 时标记为 skipped。
//
// 每个用例在独立临时工作目录里跑，互不影响；结果可导出 Markdown / JSON。
package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"win7harness/internal/codeidx"
	"win7harness/internal/core"
	"win7harness/internal/skills"
	"win7harness/internal/tools"
)

// 用例层。
const (
	KindBase  = "base"
	KindModel = "model"
)

// 用例状态。
const (
	StatusPass = "pass"
	StatusFail = "fail"
	StatusSkip = "skip"
)

// Case 一个评测用例。
type Case struct {
	ID   string
	Name string
	Kind string
	// NeedModel 为 true 时，没有模型配置就跳过。
	NeedModel bool
	Fn        func(*Env) error
}

// Env 用例运行环境。
type Env struct {
	Dir string
	Ctx *tools.Context
	// Log 记录用例内部步骤，失败时一起打印。
	Log []string
}

func (e *Env) note(format string, a ...interface{}) {
	e.Log = append(e.Log, fmt.Sprintf(format, a...))
}

// Path 返回工作目录下的绝对路径。
func (e *Env) Path(rel string) string {
	return filepath.Join(e.Dir, filepath.FromSlash(rel))
}

// call 调用一个工具，返回输出文本。
func (e *Env) call(name string, args map[string]interface{}) (string, error) {
	t, ok := tools.Find(name)
	if !ok {
		return "", fmt.Errorf("工具 %s 不存在", name)
	}
	res, err := t.Run(e.Ctx, args)
	if err != nil {
		return "", fmt.Errorf("%s 失败：%w", name, err)
	}
	return res.Output, nil
}

func (e *Env) mustCall(name string, args map[string]interface{}) error {
	out, err := e.call(name, args)
	if err != nil {
		return err
	}
	e.note("%s -> %.120s", name, strings.ReplaceAll(out, "\n", " / "))
	return nil
}

// Result 单个用例结果。
type Result struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Ms     int64  `json:"ms"`
}

// Report 一次评测的完整结果。
type Report struct {
	At      string   `json:"at"`
	Version string   `json:"version"`
	Results []Result `json:"results"`
	Pass    int      `json:"pass"`
	Fail    int      `json:"fail"`
	Skip    int      `json:"skip"`
	TotalMs int64    `json:"total_ms"`
}

// Score 返回通过率（不含跳过），0~1。
func (r Report) Score() float64 {
	run := r.Pass + r.Fail
	if run == 0 {
		return 0
	}
	return float64(r.Pass) / float64(run)
}

// Cases 返回全部用例。
func Cases() []Case {
	return []Case{
		{ID: "B01", Name: "写文件后内容逐字节正确", Kind: KindBase, Fn: caseWriteFile},
		{ID: "B02", Name: "精确替换只改目标片段", Kind: KindBase, Fn: caseEditFile},
		{ID: "B03", Name: "补丁 dry_run 不落盘、正式应用生效", Kind: KindBase, Fn: caseApplyPatch},
		{ID: "B04", Name: "grep 命中行号准确", Kind: KindBase, Fn: caseGrep},
		{ID: "B05", Name: "大文件按 offset/limit 分段读取", Kind: KindBase, Fn: caseReadRange},
		{ID: "B06", Name: "索引增量：写完文件后符号与行号同步", Kind: KindBase, Fn: caseIndexIncremental},
		{ID: "B07", Name: "命令执行返回真实输出与退出状态", Kind: KindBase, Fn: caseRunCommand},
		{ID: "B08", Name: "技能可被发现并加载正文", Kind: KindBase, Fn: caseSkill},
		{ID: "B09", Name: "删除后工作区与索引一致", Kind: KindBase, Fn: caseDelete},
	}
}

// Run 执行全部用例。tmpRoot 为存放各用例工作目录的父目录。
// NeedModel 用例在这里统一跳过：自动基线不驱动真实模型，端到端任务由人工/CI 单独执行。
func Run(tmpRoot string, modelReady bool) Report {
	rep := Report{At: time.Now().Format("2006-01-02 15:04:05"), Version: CurrentVersion()}
	start := time.Now()
	for _, c := range Cases() {
		if c.NeedModel && !modelReady {
			rep.Results = append(rep.Results, Result{
				ID: c.ID, Name: c.Name, Kind: c.Kind, Status: StatusSkip,
				Detail: "未配置模型 API Key，跳过端到端用例",
			})
			rep.Skip++
			continue
		}
		res := runOne(c, tmpRoot)
		rep.Results = append(rep.Results, res)
		switch res.Status {
		case StatusPass:
			rep.Pass++
		case StatusFail:
			rep.Fail++
		default:
			rep.Skip++
		}
	}
	rep.TotalMs = time.Since(start).Milliseconds()
	return rep
}

func runOne(c Case, tmpRoot string) Result {
	res := Result{ID: c.ID, Name: c.Name, Kind: c.Kind}
	dir := filepath.Join(tmpRoot, c.ID)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		res.Status = StatusFail
		res.Detail = "无法创建临时工作目录：" + err.Error()
		return res
	}
	env := &Env{Dir: dir}
	env.Ctx = &tools.Context{
		WorkDir: dir,
		Confirm: func(string, string) bool { return true },
	}
	// 每个用例从干净索引开始，避免互相污染。
	codeidx.Reset()

	t0 := time.Now()
	err := func() (e error) {
		defer func() {
			if r := recover(); r != nil {
				e = fmt.Errorf("用例内部异常：%v", r)
			}
		}()
		return c.Fn(env)
	}()
	res.Ms = time.Since(t0).Milliseconds()
	if err != nil {
		res.Status = StatusFail
		detail := err.Error()
		if len(env.Log) > 0 {
			detail += "\n步骤：" + strings.Join(env.Log, "\n")
		}
		res.Detail = detail
		return res
	}
	res.Status = StatusPass
	res.Detail = strings.Join(env.Log, "\n")
	return res
}

// ---- 用例实现 ----

func caseWriteFile(e *Env) error {
	body := "package demo\n\nfunc Alpha() int { return 1 }\n"
	if err := e.mustCall("write_file", map[string]interface{}{
		"path": "demo/a.go", "content": body,
	}); err != nil {
		return err
	}
	got, err := os.ReadFile(e.Path("demo/a.go"))
	if err != nil {
		return fmt.Errorf("文件未落盘：%w", err)
	}
	if string(got) != body {
		return fmt.Errorf("内容不一致，期望 %d 字节，实际 %d 字节", len(body), len(got))
	}
	return nil
}

func caseEditFile(e *Env) error {
	src := "a = 1\nb = 2\na = 1\n"
	if err := e.mustCall("write_file", map[string]interface{}{
		"path": "cfg.txt", "content": src,
	}); err != nil {
		return err
	}
	if err := e.mustCall("edit_file", map[string]interface{}{
		"path": "cfg.txt", "old_string": "b = 2", "new_string": "b = 22",
	}); err != nil {
		return err
	}
	got, err := os.ReadFile(e.Path("cfg.txt"))
	if err != nil {
		return err
	}
	want := "a = 1\nb = 22\na = 1\n"
	if string(got) != want {
		return fmt.Errorf("替换结果不符：%q", string(got))
	}
	return nil
}

func caseApplyPatch(e *Env) error {
	if err := e.mustCall("write_file", map[string]interface{}{
		"path": "main.go", "content": "package main\n\nfunc main() {\n\tprintln(\"old\")\n}\n",
	}); err != nil {
		return err
	}
	patch := "*** Begin Patch\n*** Update File: main.go\n@@ func main\n-\tprintln(\"old\")\n+\tprintln(\"new\")\n*** End Patch\n"
	if err := e.mustCall("apply_patch", map[string]interface{}{
		"patch": patch, "dry_run": true,
	}); err != nil {
		return err
	}
	if got, _ := os.ReadFile(e.Path("main.go")); strings.Contains(string(got), `"new"`) {
		return fmt.Errorf("dry_run 竟然写盘了")
	}
	if err := e.mustCall("apply_patch", map[string]interface{}{"patch": patch}); err != nil {
		return err
	}
	got, err := os.ReadFile(e.Path("main.go"))
	if err != nil {
		return err
	}
	if !strings.Contains(string(got), `println("new")`) {
		return fmt.Errorf("补丁未生效：%q", string(got))
	}
	return nil
}

func caseGrep(e *Env) error {
	if err := e.mustCall("write_file", map[string]interface{}{
		"path": "srv/hand.go", "content": "package srv\n\n// Target 标记\nfunc Target() {}\n",
	}); err != nil {
		return err
	}
	out, err := e.call("grep", map[string]interface{}{"pattern": "func Target", "path": "."})
	if err != nil {
		return err
	}
	if !strings.Contains(out, "hand.go:4") {
		return fmt.Errorf("行号不对，期望 hand.go:4，实得：%s", strings.TrimSpace(out))
	}
	return nil
}

func caseReadRange(e *Env) error {
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "line-%03d\n", i)
	}
	if err := e.mustCall("write_file", map[string]interface{}{
		"path": "big.txt", "content": b.String(),
	}); err != nil {
		return err
	}
	out, err := e.call("read_file", map[string]interface{}{
		"path": "big.txt", "offset": 100, "limit": 5,
	})
	if err != nil {
		return err
	}
	if !strings.Contains(out, "line-101") || strings.Contains(out, "line-001\n") {
		return fmt.Errorf("分段读取区间不对：%.200s", out)
	}
	return nil
}

func caseIndexIncremental(e *Env) error {
	src1 := "package demo\n\nfunc Alpha() int { return 1 }\n"
	if err := e.mustCall("write_file", map[string]interface{}{
		"path": "pkg.go", "content": src1,
	}); err != nil {
		return err
	}
	ix := codeidx.Build(e.Dir, nil)
	if len(ix.Search("Alpha", 5)) == 0 {
		return fmt.Errorf("首次索引没找到 Alpha")
	}
	if len(ix.Search("Beta", 5)) != 0 {
		return fmt.Errorf("不该找到 Beta")
	}
	src2 := "package demo\n\nfunc Alpha() int { return 1 }\n\nfunc Beta() int { return 2 }\n"
	if err := core.WriteFileAtomic(e.Path("pkg.go"), []byte(src2), 0o644); err != nil {
		return err
	}
	codeidx.MarkDirtyAbs(e.Path("pkg.go"))
	st := codeidx.Update(e.Dir)
	e.note("增量统计：dirty=%d parsed=%d reused=%d", st.Dirty, st.Parsed, st.Reused)
	if st.Dirty < 1 {
		return fmt.Errorf("脏文件未被强制重解析（dirty=%d）", st.Dirty)
	}
	hits := codeidx.Cached().Search("Beta", 5)
	if len(hits) == 0 {
		return fmt.Errorf("增量更新后仍找不到 Beta（dirty=%d parsed=%d）", st.Dirty, st.Parsed)
	}
	if hits[0].Line != 5 {
		return fmt.Errorf("Beta 行号应为 5，实得 %d", hits[0].Line)
	}
	return nil
}

func caseRunCommand(e *Env) error {
	out, err := e.call("run_command", map[string]interface{}{"command": "echo harness-eval-ok"})
	if err != nil {
		return err
	}
	if !strings.Contains(out, "harness-eval-ok") {
		return fmt.Errorf("命令输出未回传：%s", strings.TrimSpace(out))
	}
	return nil
}

func caseSkill(e *Env) error {
	sd := skills.ProjectDir(e.Dir)
	if err := os.MkdirAll(filepath.Join(sd, "fix-loop"), 0o755); err != nil {
		return err
	}
	doc := "---\nname: fix-loop\ndescription: 编译失败的定位与修复\nwhen: 改动 Go 代码后\n---\n1. 先跑 build\n2. 读第一条错误\n"
	if err := core.WriteFileAtomic(filepath.Join(sd, "fix-loop", "SKILL.md"), []byte(doc), 0o644); err != nil {
		return err
	}
	list := skills.Scan(e.Dir)
	// 不能断言"只有 1 个技能"：Scan 会把项目级与**全局**技能目录合并，
	// 开发机上只要装过技能，这条就必红（B08 一直是这么"失败"的）。
	// 这里只要求本次写进去的那个技能被扫到，且正文解析正确。
	var found *skills.Skill
	for i := range list {
		if list[i].Name == "fix-loop" {
			found = &list[i]
			break
		}
	}
	if found == nil {
		return fmt.Errorf("技能扫描没找到 fix-loop（共 %d 个）：%+v", len(list), list)
	}
	if !strings.Contains(found.Body, "先跑 build") {
		return fmt.Errorf("技能正文未解析出来")
	}
	out, err := e.call("skill_use", map[string]interface{}{"name": "fix-loop"})
	if err != nil {
		return err
	}
	if !strings.Contains(out, "读第一条错误") {
		return fmt.Errorf("skill_use 未返回正文")
	}
	if blk := skills.Block(e.Dir); !strings.Contains(blk, "fix-loop") {
		return fmt.Errorf("系统提示未注入技能清单")
	}
	return nil
}

func caseDelete(e *Env) error {
	if err := e.mustCall("write_file", map[string]interface{}{
		"path": "tmp/gone.txt", "content": "bye",
	}); err != nil {
		return err
	}
	if err := e.mustCall("delete_path", map[string]interface{}{"path": "tmp/gone.txt"}); err != nil {
		return err
	}
	if _, err := os.Stat(e.Path("tmp/gone.txt")); err == nil {
		return fmt.Errorf("文件仍存在，删除未生效")
	}
	return nil
}

// Markdown 生成人类可读报告。
func (r Report) Markdown() string {
	var b strings.Builder
	b.WriteString("# WIN7 Harness 评测基线报告\n\n")
	fmt.Fprintf(&b, "- 版本：v%s\n- 时间：%s\n- 用例：%d 个（通过 %d / 失败 %d / 跳过 %d）\n",
		r.Version, r.At, len(r.Results), r.Pass, r.Fail, r.Skip)
	fmt.Fprintf(&b, "- 通过率：%.1f%%（不含跳过）\n- 总耗时：%.2f s\n\n", r.Score()*100, float64(r.TotalMs)/1000)
	b.WriteString("| 用例 | 名称 | 层 | 结果 | 耗时 |\n| --- | --- | --- | --- | --- |\n")
	for _, x := range r.Results {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %d ms |\n", x.ID, x.Name, x.Kind, statusText(x.Status), x.Ms)
	}
	var fails []Result
	for _, x := range r.Results {
		if x.Status == StatusFail {
			fails = append(fails, x)
		}
	}
	if len(fails) > 0 {
		b.WriteString("\n## 失败详情\n\n")
		for _, x := range fails {
			fmt.Fprintf(&b, "### %s %s\n\n```\n%s\n```\n\n", x.ID, x.Name, x.Detail)
		}
	}
	if r.Skip > 0 {
		b.WriteString("\n## 跳过说明\n\n")
		for _, x := range r.Results {
			if x.Status == StatusSkip {
				fmt.Fprintf(&b, "- %s %s：%s\n", x.ID, x.Name, x.Detail)
			}
		}
	}
	b.WriteString("\n> base 层不依赖模型，验证工具链与索引、技能等底座能力；model 层需配置 API Key 后执行。\n")
	return b.String()
}

func statusText(s string) string {
	switch s {
	case StatusPass:
		return "通过"
	case StatusFail:
		return "失败"
	default:
		return "跳过"
	}
}

// SortedIDs 便于测试断言用例编号连续。
func SortedIDs() []string {
	var ids []string
	for _, c := range Cases() {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids
}
