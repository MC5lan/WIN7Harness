package tools

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"win7harness/internal/core"
)

// 后台任务托管：长跑命令（dev server、watch、构建、日志抓取）不该把一轮对话卡死。
//
// 设计要点：
//   - 输出保留尾部固定窗口（默认 512KB），并记录"已丢弃字节数"，因此调用方用绝对
//     偏移增量读取时不会因为滚动而错位；
//   - 每个任务有独占的读游标是无状态的：游标由调用方传入，重复读同一段是安全的；
//   - 任务有硬寿命（默认 20 分钟，最多 1 小时），到点为防止僵尸进程一律杀掉，
//     避免 Win7 上几十个 cmd.exe 把内存吃光；
//   - 进程树清理交给平台层（Windows 用 Job Object），任务结束即释放句柄。

const (
	maxBgBuffer  = 512 * 1024
	maxBgTasks   = 8
	bgDefaultTTL = 20 * time.Minute
	bgMaxTTL     = 60 * time.Minute
	bgTailBytes  = 16 * 1024
)

type bgTask struct {
	ID      string
	Cmdline string
	Cwd     string
	Started time.Time

	mu      sync.Mutex
	buf     []byte // 尾部窗口
	dropped int64  // 已丢弃字节数，绝对偏移 = dropped + 窗口内下标
	done    bool
	code    int
	killed  bool
	note    string
	ended   time.Time

	proc *bgProc

	doneCh    chan struct{}
	closeOnce sync.Once
}

func (t *bgTask) append(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > maxBgBuffer {
		cut := len(t.buf) - maxBgBuffer
		t.dropped += int64(cut)
		t.buf = append(t.buf[:0], t.buf[cut:]...)
	}
}

func (t *bgTask) finish(code int) {
	t.mu.Lock()
	if !t.done {
		t.done = true
		t.code = code
		t.ended = time.Now()
		t.closeOnce.Do(func() { close(t.doneCh) })
	}
	t.mu.Unlock()
}

// setProc / getProc 保证 proc 字段的可见性（启动 goroutine 与 kill 路径之间）。
func (t *bgTask) setProc(p *bgProc) {
	t.mu.Lock()
	t.proc = p
	t.mu.Unlock()
}

func (t *bgTask) getProc() *bgProc {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.proc
}

// total 返回截至目前的输出总字节数。
func (t *bgTask) total() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dropped + int64(len(t.buf))
}

// readFrom 从绝对偏移 from 读取到末尾，返回文本与下一偏移。
func (t *bgTask) readFrom(from int64) (string, int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	total := t.dropped + int64(len(t.buf))
	if from < t.dropped {
		// 请求的位置已经被滚动丢弃：从窗口起点给，并标注跳过了多少。
		from = t.dropped
	}
	if from > total {
		from = total
	}
	start := int(from - t.dropped)
	return string(t.buf[start:]), total
}

func (t *bgTask) status() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.done {
		return "运行中"
	}
	if t.killed {
		return fmt.Sprintf("已终止（退出码 %d）", t.code)
	}
	return fmt.Sprintf("已结束（退出码 %d）", t.code)
}

func (t *bgTask) isDone() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done
}

// exitCode 返回任务退出码（未结束时无意义）。
func (t *bgTask) exitCode() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.code
}

var bgStore struct {
	sync.Mutex
	seq   uint32
	tasks map[string]*bgTask
	order []string
}

func bgInit() {
	bgStore.Lock()
	if bgStore.tasks == nil {
		bgStore.tasks = map[string]*bgTask{}
	}
	bgStore.Unlock()
}

// StartBackground 启动一个后台任务并返回其 ID。
func StartBackground(cmdline, cwd string, ttl time.Duration) (*bgTask, error) {
	bgInit()
	bgStore.Lock()
	active := 0
	for _, t := range bgStore.tasks {
		if !t.isDone() {
			active++
		}
	}
	if active >= maxBgTasks {
		bgStore.Unlock()
		return nil, fmt.Errorf("同时运行的后台任务已达上限 %d 个，请先 task_kill 掉不用的", maxBgTasks)
	}
	id := fmt.Sprintf("bg-%d", atomic.AddUint32(&bgStore.seq, 1))
	t := &bgTask{ID: id, Cmdline: cmdline, Cwd: cwd, Started: time.Now(), doneCh: make(chan struct{})}
	bgStore.tasks[id] = t
	bgStore.order = append(bgStore.order, id)
	// 淘汰最老的已完成任务：已完成的最多保留 50 条，防止长期运行内存缓慢增长
	const keepFinished = 50
	finished := make([]string, 0, len(bgStore.order))
	for _, fid := range bgStore.order {
		if ft, ok := bgStore.tasks[fid]; ok && ft.isDone() {
			finished = append(finished, fid)
		}
	}
	for len(finished) > keepFinished {
		victim := finished[0]
		finished = finished[1:]
		delete(bgStore.tasks, victim)
		for i, x := range bgStore.order {
			if x == victim {
				bgStore.order = append(bgStore.order[:i], bgStore.order[i+1:]...)
				break
			}
		}
	}
	bgStore.Unlock()

	if ttl <= 0 {
		ttl = bgDefaultTTL
	}
	if ttl > bgMaxTTL {
		ttl = bgMaxTTL
	}

	p, err := startBgProc(cmdline, cwd, t.append)
	if err != nil {
		t.finish(-1)
		return t, err
	}
	t.setProc(p)
	core.SafeGo("tools/bgtask.go:167", func() {
		code := p.wait()
		t.finish(code)
	})
	// 硬寿命：到点强杀整棵进程树，防止被遗忘的 dev server 常驻。
	core.SafeGo("tools/bgtask.go:172", func() {
		timer := time.NewTimer(ttl)
		defer timer.Stop()
		select {
		case <-timer.C:
			if !t.isDone() {
				t.mu.Lock()
				t.killed = true
				t.note = fmt.Sprintf("超过最长运行时间 %s，已强制终止", ttl)
				t.mu.Unlock()
				p.kill()
			}
		case <-t.doneCh:
		}
	})
	return t, nil
}

func findBg(id string) (*bgTask, bool) {
	bgInit()
	bgStore.Lock()
	defer bgStore.Unlock()
	t, ok := bgStore.tasks[strings.TrimSpace(id)]
	return t, ok
}

func listBg() []*bgTask {
	bgInit()
	bgStore.Lock()
	defer bgStore.Unlock()
	out := make([]*bgTask, 0, len(bgStore.order))
	for _, id := range bgStore.order {
		if t, ok := bgStore.tasks[id]; ok {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

func init() {
	register(Tool{
		Name: "task_output",
		Desc: "读取后台任务的输出。不传 from 时返回最近的尾部内容（最多 16KB）；传 from 则从该偏移增量读取，用于轮询长跑任务。返回值带任务状态与下一次应传的 from。",
		Schema: `{
  "type":"object",
  "properties":{
    "id":{"type":"string","description":"后台任务 ID，如 bg-1"},
    "from":{"type":"integer","description":"从第几个字节开始读（绝对偏移）。首次可省略，之后用上次返回的 next 值"},
    "max_chars":{"type":"integer","description":"本次最多返回的字符数，默认 16000"}
  },
  "required":["id"]
}`,
		Run: runTaskOutput,
	})
	register(Tool{
		Name:   "task_list",
		Desc:   "列出所有后台任务及其状态（运行中/已结束）、退出码、启动时间与命令。",
		Schema: `{"type":"object","properties":{}}`,
		Run:    runTaskList,
	})
	register(Tool{
		Name: "task_kill",
		Desc: "终止一个后台任务（连同其子进程）。用于清理 dev server、watch 等常驻进程。",
		Schema: `{
  "type":"object",
  "properties":{"id":{"type":"string","description":"后台任务 ID"}},
  "required":["id"]
}`,
		Run: runTaskKill,
	})
}

// BackgroundCount 还在跑的后台任务数（状态页用）。
func BackgroundCount() int {
	n := 0
	bgStore.Lock()
	defer bgStore.Unlock()
	for _, t := range bgStore.tasks {
		if st := t.status(); st == "运行中" {
			n++
		}
	}
	return n
}

// BackgroundInfo 是给界面展示的后台任务快照。
type BackgroundInfo struct {
	ID      string
	Cmdline string
	Cwd     string
	Started time.Time
	Ended   time.Time
	Done    bool
	Killed  bool
	Code    int
	Note    string
	Bytes   int64
	Status  string
}

// BackgroundTasks 返回全部后台任务（运行中在前，同状态按启动时间排序）。
func BackgroundTasks() []BackgroundInfo {
	all := listBg()
	out := make([]BackgroundInfo, 0, len(all))
	for _, t := range all {
		t.mu.Lock()
		info := BackgroundInfo{
			ID: t.ID, Cmdline: t.Cmdline, Cwd: t.Cwd, Started: t.Started,
			Ended: t.ended, Done: t.done, Killed: t.killed, Code: t.code,
			Note: t.note, Bytes: t.dropped + int64(len(t.buf)),
		}
		t.mu.Unlock()
		info.Status = t.status()
		out = append(out, info)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Done != out[j].Done {
			return !out[i].Done
		}
		return out[i].Started.After(out[j].Started)
	})
	return out
}

// KillBackground 从界面终止一个后台任务：只发出终止信号并立刻返回，
// 真正的进程树清理在后台完成，界面不会因为 taskkill 卡住。
func KillBackground(id string) bool {
	t, ok := findBg(strings.TrimSpace(id))
	if !ok || t.isDone() {
		return false
	}
	t.mu.Lock()
	t.killed = true
	t.note = "用户从后台任务页终止"
	t.mu.Unlock()
	if p := t.getProc(); p != nil {
		core.SafeGo("tools/bgtask.go:killUI", func() { p.kill() })
	}
	return true
}

func runTaskOutput(ctx *Context, args map[string]interface{}) (Result, error) {
	id := strings.TrimSpace(Get(args, "id"))
	t, ok := findBg(id)
	if !ok {
		return Result{}, fmt.Errorf("不存在后台任务 %s（用 task_list 查看）", id)
	}
	maxChars := GetInt(args, "max_chars", 16000)
	if maxChars <= 0 || maxChars > 64000 {
		maxChars = 16000
	}
	fromRaw, hasFrom := args["from"]
	var text string
	var next int64
	if hasFrom {
		from := int64(0)
		switch v := fromRaw.(type) {
		case float64:
			from = int64(v)
		case int:
			from = int64(v)
		case string:
			if _, err := fmt.Sscanf(v, "%d", &from); err != nil {
				return Result{}, fmt.Errorf("from 参数无法解析为数字：%q", v)
			}
		default:
			return Result{}, fmt.Errorf("from 参数类型不支持：%T", fromRaw)
		}
		if from < 0 {
			return Result{}, fmt.Errorf("from 不能为负数，实际 %d", from)
		}
		text, next = t.readFrom(from)
	} else {
		total := t.total()
		start := total - bgTailBytes
		if start < 0 {
			start = 0
		}
		text, next = t.readFrom(start)
	}
	if len(text) > maxChars {
		// 超长时保留尾部：报错信息通常在最后。
		text = text[len(text)-maxChars:]
		text = "…（前部省略）\n" + text
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("任务 %s · %s\n命令：%s\n", t.ID, t.status(), t.Cmdline))
	t.mu.Lock()
	note := t.note
	t.mu.Unlock()
	if note != "" {
		b.WriteString("备注：" + note + "\n")
	}
	b.WriteString(fmt.Sprintf("输出偏移：%d → %d（下次传 from=%d）\n\n", next-int64(len(text)), next, next))
	if strings.TrimSpace(text) == "" {
		b.WriteString("（暂无新输出）")
	} else {
		b.WriteString(text)
	}
	return Result{Output: b.String()}, nil
}

func runTaskList(ctx *Context, args map[string]interface{}) (Result, error) {
	all := listBg()
	if len(all) == 0 {
		return Result{Output: "当前没有后台任务。"}, nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("后台任务 %d 个：\n", len(all)))
	for _, t := range all {
		b.WriteString(fmt.Sprintf("\n%s · %s · 启动于 %s\n  %s\n",
			t.ID, t.status(), t.Started.Format("15:04:05"), t.Cmdline))
	}
	b.WriteString("\n查看输出：task_output(id) · 终止：task_kill(id)")
	return Result{Output: b.String()}, nil
}

func runTaskKill(ctx *Context, args map[string]interface{}) (Result, error) {
	id := strings.TrimSpace(Get(args, "id"))
	t, ok := findBg(id)
	if !ok {
		return Result{}, fmt.Errorf("不存在后台任务 %s", id)
	}
	if t.isDone() {
		return Result{Output: fmt.Sprintf("任务 %s 已结束，无需终止。", id)}, nil
	}
	risk := core.CommandRisk(t.Cmdline)
	if risk >= core.RiskCaution && !ctx.PreApproved {
		if ctx.Confirm == nil {
			// fail-closed：没有确认通道就不杀——此前是静默放行
			return Result{}, fmt.Errorf("终止该后台任务需要用户确认，但当前没有确认通道（子代理/无人值守）：%s", t.Cmdline)
		}
		if !ctx.Confirm("终止后台任务", "命令：\n"+t.Cmdline) {
			return Result{}, fmt.Errorf("用户取消终止")
		}
	}
	t.mu.Lock()
	t.killed = true
	t.note = "用户终止"
	t.mu.Unlock()
	if p := t.getProc(); p != nil {
		p.kill()
	}
	// 等状态收敛再回话。kill 是异步的：进程退出后还要等管道 EOF，
	// Wait 才会返回。过去"发出信号"就直接回复"已终止"，实际进程还活着，
	// 用户下一次 task_list 看到的是"运行中"——工具在这件事上说谎，比杀不掉更糟。
	// 这里最多等 3 秒；仍未退出就照实说，不再假报成功。
	for i := 0; i < 60; i++ {
		if t.isDone() {
			return Result{Output: fmt.Sprintf("已终止任务 %s（进程树一并结束，退出码 %d）。", id, t.exitCode())}, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return Result{Output: fmt.Sprintf("已向任务 %s 发出终止信号，但进程在 3 秒内仍未退出。"+
		"可能卡在不可中断的系统调用里，稍后用 task_list 再确认。", id)}, nil
}

// startBackgroundCommand 是 run_command 的 background 分支。
func startBackgroundCommand(ctx *Context, cmdline, cwd string, ttl time.Duration) (Result, error) {
	if ctx.Progress != nil {
		ctx.Progress("$ " + cmdline + "  （后台）")
	}
	t, err := StartBackground(cmdline, cwd, ttl)
	if err != nil {
		return Result{}, err
	}
	// 给一拍时间，让"命令当场就报错"的情况能在返回里直接看到。
	time.Sleep(300 * time.Millisecond)
	out, _ := t.readFrom(0)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("已在后台启动 %s（%s）\n状态：%s\n", t.ID, t.Cmdline, t.status()))
	if strings.TrimSpace(out) != "" {
		b.WriteString("\n初始输出：\n" + clip(out, 2000))
	}
	b.WriteString("\n\n用 task_output(id) 读增量输出，task_kill(id) 终止。任务最长运行 20 分钟。")
	return Result{Output: b.String()}, nil
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
