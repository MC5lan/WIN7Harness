//go:build windows

package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"win7harness/internal/agent"
	"win7harness/internal/core"
	"win7harness/internal/llm"
)

// 会话落盘版本号。将来结构变更时按此字段做迁移，避免旧档读崩。
const convFileVersion = 1

// convGzipMin 超过这个体积的会话改存 gzip。
// 长对话的 JSON 里大量重复的字段名与工具输出，压缩比通常在 5~8 倍，
// 而几十 KB 的小会话压了也省不了多少，还多一次解压开销。
const convGzipMin = 96 << 10

// convWriteMu 串行化会话写盘：自动保存、手动改名、退出时保存可能来自不同 goroutine，
// 并发写同一文件会互相踩踏（.tmp 被 rename 掉）。
var convWriteMu sync.Mutex

// convSaveQueue 把"序列化 + gzip + 备份 + 写盘"从 UI 线程挪到后台。
//
// 为什么必须异步：流式输出每 400ms 触发一次 saveConv，长会话的 JSON 有几 MB，
// gzip 与旧文件备份又各读一次盘。放在 UI 线程上，滚轮/打字会周期性卡一下，
// 大仓库 + 杀软环境甚至能卡到几百毫秒。
//
// 队列按会话 ID 合并：同一会话连续保存只写最新一份；不同会话按入队顺序写。
type convSaveQueue struct {
	mu      sync.Mutex
	pending map[string]*Conv
	order   []string
	wake    chan struct{}
	done    chan struct{}
	idle    chan struct{}
	idleOK  bool // idle 已关闭（队列空闲）
	closed  bool
}

func newConvSaveQueue() *convSaveQueue {
	q := &convSaveQueue{
		pending: map[string]*Conv{},
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		idle:    make(chan struct{}),
	}
	close(q.idle)
	q.idleOK = true
	return q
}

func (q *convSaveQueue) enqueue(cv *Conv) {
	if cv == nil || cv.ID == "" {
		return
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	if q.idleOK {
		q.idle = make(chan struct{})
		q.idleOK = false
	}
	if _, exists := q.pending[cv.ID]; !exists {
		q.order = append(q.order, cv.ID)
	}
	q.pending[cv.ID] = cv
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *convSaveQueue) loop(a *App) {
	defer close(q.done)
	for {
		select {
		case <-q.wake:
		case <-q.done:
			return
		}
		for {
			q.mu.Lock()
			if len(q.order) == 0 {
				if !q.idleOK {
					close(q.idle)
					q.idleOK = true
				}
				stop := q.closed
				q.mu.Unlock()
				if stop {
					return
				}
				break
			}
			id := q.order[0]
			q.order = q.order[1:]
			cv := q.pending[id]
			delete(q.pending, id)
			q.mu.Unlock()
			if cv != nil {
				a.persistConv(cv)
			}
		}
	}
}

// flush 等待队列清空；超时返回 false（调用方可再做一次同步落盘兜底）。
func (q *convSaveQueue) flush(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		q.mu.Lock()
		if len(q.order) == 0 && q.idleOK {
			q.mu.Unlock()
			return true
		}
		idle := q.idle
		q.mu.Unlock()
		remain := time.Until(deadline)
		if remain <= 0 {
			return false
		}
		timer := time.NewTimer(remain)
		select {
		case <-idle:
			timer.Stop()
		case <-timer.C:
			return false
		}
	}
}

// stop 停止接收新任务并等待队列写完。只能调用一次。
func (q *convSaveQueue) stop(timeout time.Duration) bool {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
	ok := q.flush(timeout)
	return ok
}

// snapshotConv 复制一份可安全交给后台序列化的会话快照。
// 消息与工具卡都是指针/切片，直接交给 goroutine 会与 UI 线程的流式追加竞态；
// 复制本身是 O(n) 的内存拷贝，远低于 JSON + gzip + 磁盘 I/O 的成本。
func snapshotConv(cv *Conv) *Conv {
	if cv == nil {
		return nil
	}
	out := *cv
	if len(cv.Msgs) > 0 {
		out.Msgs = make([]Message, len(cv.Msgs))
		copy(out.Msgs, cv.Msgs)
	} else {
		// nil slice 会被 encoding/json 写成 "msgs":null，下次启动的会话索引
		// 会把它当成坏文件（“msgs 不是数组”）。空会话统一写 []。
		out.Msgs = []Message{}
	}
	for i := range out.Msgs {
		m := &out.Msgs[i]
		if len(m.Tools) > 0 {
			tools := make([]*ToolCard, len(m.Tools))
			for j, tc := range m.Tools {
				if tc == nil {
					continue
				}
				c := *tc
				tools[j] = &c
			}
			m.Tools = tools
		}
		if len(m.Images) > 0 {
			m.Images = append([]string(nil), m.Images...)
		}
		if len(m.Hits) > 0 {
			m.Hits = append([]HitView(nil), m.Hits...)
		}
	}
	return &out
}

func (a *App) queueConvSave(cv *Conv) {
	if a.saver == nil {
		a.persistConv(snapshotConv(cv))
		return
	}
	a.saver.enqueue(snapshotConv(cv))
}

// convFile 是会话文件的外壳，便于加版本与元信息而不污染 Conv 本身。
type convFile struct {
	V    int   `json:"v"`
	Conv *Conv `json:"conv"`
}

// newConvID 生成会话 ID。用纳秒时间戳的十六进制，天然有序且无需外部依赖。
func newConvID() string {
	return fmt.Sprintf("%016x", time.Now().UnixNano())
}

// convPaths 返回某会话可能存在的三种文件：明文、gzip、崩溃前的备份。
func convPaths(id string) (plain, gz, bak string) {
	base := filepath.Join(core.SessionsDir(), id)
	return base + ".json", base + ".json.gz", base + ".json.bak"
}

// convPath 返回某会话的主文件路径（明文），仅用于展示与兼容旧逻辑。
func convPath(id string) string {
	p, _, _ := convPaths(id)
	return p
}

// writeFileAtomic 先写 .tmp 再 rename，避免掉电/崩溃留下半截 JSON。
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := core.WriteFileAtomic(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// encodeConv 把会话序列化，体积大的自动 gzip（压缩后反而更大则存明文）。
func encodeConv(cv *Conv) ([]byte, error) {
	data, err := json.Marshal(convFile{V: convFileVersion, Conv: cv})
	if err != nil {
		return nil, err
	}
	if gz := core.GzipIfSmaller(data, convGzipMin); gz != nil {
		return gz, nil
	}
	return data, nil
}

// decodeConv 解析会话文件，自动识别是否 gzip。
func decodeConv(data []byte) (*Conv, error) {
	// 解压上限 128MB，挡住畸形文件把 32 位进程的内存打爆
	if out, err := core.GunzipData(data, 128<<20); err == nil {
		data = out
	} else {
		return nil, err
	}
	var cf convFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, err
	}
	if cf.Conv == nil {
		return nil, fmt.Errorf("会话内容为空")
	}
	return cf.Conv, nil
}

// persistConv 把单个会话写盘（带备份与后缀切换）。调用方负责加锁语义外的业务前置。
func (a *App) persistConv(cv *Conv) {
	if cv == nil {
		return
	}
	if cv.ID == "" {
		cv.ID = newConvID()
	}
	deriveTitle(cv)
	data, err := encodeConv(cv)
	if err != nil {
		core.Logf("序列化会话失败: %v", err)
		return
	}
	if err := os.MkdirAll(core.SessionsDir(), 0o755); err != nil {
		core.Logf("创建会话目录失败: %v", err)
		return
	}

	convWriteMu.Lock()
	defer convWriteMu.Unlock()

	plain, gz, bak := convPaths(cv.ID)
	target, other := plain, gz
	if core.IsGzipData(data) {
		target, other = gz, plain
	}

	// 写前留一份上一版：文件系统层面的损坏（半截写、杀软锁文件）靠它可以救回一次。
	// v1.29：备份失败不再静默吞掉——备份没写成还照写主文件，等于把兜底悄悄摘了，
	// 至少要留下日志，出问题时能看出"这次覆盖是没有后悔药的那次"。
	backupOld := func(src string) bool {
		old, rerr := os.ReadFile(src)
		if rerr != nil || len(old) == 0 {
			return false
		}
		if err := writeFileAtomic(bak, old); err != nil {
			core.Logf("会话备份写入失败(%s): %v", filepath.Base(src), err)
		}
		return true
	}
	if !backupOld(target) {
		backupOld(other)
	}

	if err := writeFileAtomic(target, data); err != nil {
		core.Logf("写入会话失败: %v", err)
		return
	}
	// 体积跨过阈值后新旧后缀只留一个，避免下次装载读到过期副本
	if err := os.Remove(other); err != nil && !os.IsNotExist(err) {
		core.Logf("清理旧会话文件失败: %v", err)
	}
}

// readConvByID 读取指定会话，主文件坏了就退回 .bak。
func readConvByID(id string) (*Conv, error) {
	plain, gz, bak := convPaths(id)
	var lastErr error
	for _, p := range []string{gz, plain, bak} {
		data, err := os.ReadFile(p)
		if err != nil {
			if !os.IsNotExist(err) {
				lastErr = err
			}
			continue
		}
		cv, derr := decodeConv(data)
		if derr != nil {
			lastErr = fmt.Errorf("%s: %v", filepath.Base(p), derr)
			continue
		}
		if strings.HasSuffix(p, ".bak") {
			core.Logf("会话 %s 主文件损坏，已从备份恢复", id)
		}
		return cv, nil
	}
	if lastErr == nil {
		lastErr = os.ErrNotExist
	}
	return nil, lastErr
}

// loadAllConvs 扫描会话目录，按"置顶优先 + 最近活动倒序"装载。
//
// v1.31.1 起启动不再逐条完整解析所有会话：先用流式索引拿到标题/条数/预览，
// 只把当前要显示的最近会话读入内存；其余空壳在后台补全前 N 条，更老的等
// 用户点开再读。这样历史库很大时窗口也能先出来，不会“启动先卡几秒”。
func (a *App) loadAllConvs() {
	dir := core.SessionsDir()
	briefs, err := core.SessionIndex(dir, 5000)
	if err != nil {
		core.Logf("读取会话索引失败: %v", err)
	}
	list := make([]*Conv, 0, len(briefs))
	for _, b := range briefs {
		title := strings.TrimSpace(b.Title)
		if title == "" {
			title = "未命名对话"
		}
		list = append(list, &Conv{
			ID: b.ID, Title: title, At: b.At, Pinned: b.Pinned,
			MsgN: b.Msgs, Preview: b.Preview,
		})
	}
	sortConvs(list)
	if len(list) == 0 {
		list = []*Conv{{ID: newConvID(), Title: "新对话", At: time.Now(), Msgs: []Message{}, Loaded: true}}
		a.convs = list
		a.active = 0
		core.Logf("已装载 0 个会话，已创建新会话")
		return
	}

	// 只同步读当前要显示的最近会话；失败时保留索引空壳，后续点开再重试。
	if cv, rerr := readConvByID(list[0].ID); rerr != nil {
		core.Logf("当前会话 %s 读取失败: %v", list[0].ID, rerr)
	} else if cv != nil {
		cv.Loaded = true
		cv.MsgN = len(cv.Msgs)
		cv.Preview = convLastPreview(cv)
		list[0] = cv
	}
	a.convs = list
	a.active = 0

	// 后台补全最近 24 条，搜索/切换时不用再等磁盘；更老的保持空壳。
	ids := make([]string, 0, 24)
	for _, cv := range list[1:] {
		if len(ids) >= 24 {
			break
		}
		ids = append(ids, cv.ID)
	}
	if len(ids) > 0 {
		core.SafeGo("app/store.go:hydrate", func() { a.hydrateConvs(ids) })
	}
	core.Logf("已装载 %d 个会话（当前会话已读入，其余后台补全）", len(list))
}

// deriveTitle 用首条用户消息生成会话标题。
// 左侧会话列表如果全长一个样（"新对话"），翻历史基本没法用，所以落盘前补一次。
func deriveTitle(cv *Conv) {
	t := strings.TrimSpace(cv.Title)
	if t != "" && t != "新对话" && t != "未命名对话" {
		return // 已经有真标题了，不覆盖
	}
	for _, m := range cv.Msgs {
		if m.Role != "user" {
			continue
		}
		line := strings.TrimSpace(m.Text)
		// 只取第一行，避免多行输入把列表撑爆
		if i := strings.IndexAny(line, "\r\n"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		r := []rune(line)
		if len(r) > 22 {
			cv.Title = string(r[:22]) + "…"
		} else {
			cv.Title = string(r)
		}
		return
	}
}

// saveConvFile 只把指定会话写盘，用于置顶、改名这类不涉及消息缓冲的改动。
func (a *App) saveConvFile(cv *Conv) {
	if cv == nil || !cv.Loaded {
		return
	}
	a.queueConvSave(cv)
}

// saveConv 把当前会话写盘。序列化与落盘在后台队列完成，UI 线程只做指针拷贝。
func (a *App) saveConv() {
	cv := a.currentConv()
	if cv == nil || !cv.Loaded {
		return
	}
	cv.Msgs = a.msgs
	cv.At = time.Now()
	cv.MsgN = len(a.msgs)
	// 生成中会把完整引擎历史每 400ms 复制一遍，代价太高；等本轮结束再同步。
	// 中途崩溃由 journal 兜底，下一次正常保存会补上结构化轨迹。
	if !a.streaming {
		a.syncEngineTranscript(cv)
	}
	a.queueConvSave(cv)
}

// syncEngineTranscript 把引擎的结构化消息（含工具调用/结果）快照进会话，
// 供重启/切模型/切工作区后恢复。只保留最近一段，避免会话文件无限膨胀。
func (a *App) syncEngineTranscript(cv *Conv) {
	if cv == nil || a.eng == nil {
		return
	}
	maxBytes := 1 << 20
	maxTokens := 60_000
	// 64 位进程在大窗口下保留更长的结构化轨迹；32 位仍守 1MB，
	// 避免会话序列化和内存复制把地址空间打爆。
	if strconv.IntSize == 64 {
		maxBytes = 2 << 20
		maxTokens = 120_000
		if a.cfg != nil && a.cfg.ContextWindow >= 300000 {
			maxTokens = 200_000
		}
	}
	cv.EngineMsgs = pruneEngineMsgs(a.eng.History(), 320, maxBytes, maxTokens)
}

// pruneEngineMsgs 保留最近 N 条/字节/token 三者中最严的一段结构化消息，
// 并把图片 data URL 去掉。从尾部向前取，保证最后一轮工具调用/Tool 结果成对；
// 起点落在 tool 上时向前对齐。
func pruneEngineMsgs(msgs []llm.Message, maxN, maxBytes, maxTokens int) []llm.Message {
	if len(msgs) == 0 {
		return nil
	}
	start := len(msgs)
	totalBytes := 0
	totalTokens := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		l := len(msgs[i].Content) + len(msgs[i].Reasoning)
		for _, tc := range msgs[i].ToolCalls {
			l += len(tc.Function.Arguments)
		}
		for _, p := range msgs[i].Parts {
			if p.Type == "image_url" {
				l += 32
			} else {
				l += len(p.Text)
			}
		}
		tok := agent.EstimateMessageTokens(&msgs[i])
		if start < len(msgs) && (len(msgs)-i > maxN || totalBytes+l > maxBytes || (maxTokens > 0 && totalTokens+tok > maxTokens)) {
			break
		}
		totalBytes += l
		totalTokens += tok
		start = i
	}
	for start > 0 && msgs[start].Role == "tool" {
		start--
	}
	out := make([]llm.Message, len(msgs)-start)
	copy(out, msgs[start:])
	for i := range out {
		if len(out[i].Parts) == 0 {
			continue
		}
		// 必须新建切片：out 与引擎历史共享底层数组，原地过滤会把在线上下文里的
		// 图片也一起删掉（下一轮 hasImagesInContext 就会误判）。
		kept := make([]llm.Part, 0, len(out[i].Parts))
		stripped := false
		for _, p := range out[i].Parts {
			if p.Type == "image_url" {
				stripped = true
				continue
			}
			kept = append(kept, p)
		}
		out[i].Parts = kept
		if stripped {
			out[i].Content += "\n（历史图片已省略，需要时请重新附加）"
		}
	}
	return out
}

// deleteConvByID 按稳定 ID 删除：确认弹窗打开期间列表可能重新排序，
// 用下标删除会误删另一个会话。
func (a *App) deleteConvByID(id string) {
	if i := a.convIndexByID(id); i >= 0 {
		a.deleteConv(i)
	}
}

// deleteConv 删除指定会话的文件，并在必要时补一个空会话。
func (a *App) deleteConv(i int) {
	if i < 0 || i >= len(a.convs) {
		return
	}
	// 先等异步保存队列清空再删文件：否则一个已经排队的旧快照可能在删除后
	// 又被写回磁盘，用户会看到"删掉的会话自己回来了"。
	if a.saver != nil {
		a.saver.flush(2 * time.Second)
	}
	cv := a.convs[i]
	if cv.ID != "" {
		plain, gz, bak := convPaths(cv.ID)
		for _, p := range []string{plain, gz, bak} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				core.Logf("删除会话文件失败: %v", err)
			}
		}
	}
	a.convs = append(a.convs[:i], a.convs[i+1:]...)
	a.sessDirty = true
	if len(a.convs) == 0 {
		a.convs = []*Conv{{ID: newConvID(), Title: "新对话", At: time.Now(), Loaded: true}}
	}
	if a.active >= len(a.convs) {
		a.active = len(a.convs) - 1
	}
	if a.active < 0 {
		a.active = 0
	}
	a.loadConv()
}

// totalConvs 返回会话数量。
func (a *App) totalConvs() int { return len(a.convs) }
