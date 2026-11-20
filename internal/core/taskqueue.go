package core

import (
	"sync"
	"time"
)

// 任务队列：v1.28 及之前，定时任务、目录守护、手机指令共用一条"正在跑"的判断，
// 谁先占了引擎，后面到点的任务就在循环里 break 掉——静默丢活。
// 这里给它们一条排队通道：到点就入队，引擎空了再按先后顺序取。

// TaskItem 队列中的一项。
type TaskItem struct {
	ID     string    `json:"id"`
	Source string    `json:"source"` // schedule / watch / remote / manual
	Prompt string    `json:"prompt"`
	Note   string    `json:"note,omitempty"`  // 给用户看的一句话说明
	Dedup  string    `json:"dedup,omitempty"` // 去重键：同一到期点只入队一次
	At     time.Time `json:"at"`
}

// TaskQueue 有界、去重、先进先出。零值不可用，必须 NewTaskQueue。
type TaskQueue struct {
	mu    sync.Mutex
	max   int
	items []TaskItem
	seen  map[string]time.Time
	order []string // 记录 dedup 键的插入顺序，用于淘汰
	seq   int
	kv    *KV // 可选：非 nil 时队列状态落盘，重启后恢复
}

// NewTaskQueue 创建队列，max 为上限（<=0 时取 64）。
//
// 传入 kv 后启用持久化：队列项与序号写入 KV，重启后恢复。只持久化 items/seq，
// seen/order 在 load 时从 items 重建——这样崩溃时"已 Pop、正在处理"的任务
// 不会把去重键永久钉在盘上，重启后可以重新入队（崩溃恢复的正确语义）。
// 不传 kv 或传 nil 时行为与旧版完全一致（纯内存）。
func NewTaskQueue(max int, kv ...*KV) *TaskQueue {
	if max <= 0 {
		max = 64
	}
	q := &TaskQueue{max: max, seen: map[string]time.Time{}}
	if len(kv) > 0 {
		q.kv = kv[0]
	}
	if q.kv != nil {
		q.load()
	}
	return q
}

// taskQueueState 是队列的落盘形态。
type taskQueueState struct {
	Items []TaskItem `json:"items"`
	Seq   int        `json:"seq"`
}

const taskQueueKVKey = "main"

// load 构造时从 KV 恢复队列（无并发）。写失败/坏数据都退化为内存空队列，
// 不让持久化反过来拖垮启动——这是增强，不是 correctness 依赖。
func (q *TaskQueue) load() {
	var st taskQueueState
	found, err := q.kv.Get("taskqueue", taskQueueKVKey, &st)
	if err != nil || !found {
		return
	}
	q.items = st.Items
	q.seq = st.Seq
	if len(q.items) > q.max {
		q.items = q.items[len(q.items)-q.max:] // 配置变小/旧数据超长时截掉最旧的
	}
	// 从仍在排队的项重建去重键：处理中（已 Pop）的项不在 items 里，其键不恢复。
	q.seen = map[string]time.Time{}
	q.order = nil
	for _, it := range q.items {
		if it.Dedup == "" {
			continue
		}
		if _, dup := q.seen[it.Dedup]; dup {
			continue
		}
		q.seen[it.Dedup] = it.At
		q.order = append(q.order, it.Dedup)
	}
}

// persistLocked 把队列状态写入 KV（调用方须持 q.mu）。
// 写失败只记日志、不影响队列操作——磁盘满时队列仍在内存里正常工作。
func (q *TaskQueue) persistLocked() {
	if q.kv == nil {
		return
	}
	st := taskQueueState{Items: q.items, Seq: q.seq}
	if err := q.kv.Put("taskqueue", taskQueueKVKey, st); err != nil {
		Logf("任务队列持久化失败：%v", err)
	}
}

// PushResult 描述一次入队的结果。
type PushResult struct {
	OK     bool
	Reason string // 未入队时的原因，可直接展示
	ID     string
}

// Push 入队。dedup 非空时，同一键只允许入队一次（直到被 Pop 或淘汰）。
func (q *TaskQueue) Push(it TaskItem) PushResult {
	q.mu.Lock()
	defer q.mu.Unlock()

	if it.Dedup != "" {
		if _, dup := q.seen[it.Dedup]; dup {
			return PushResult{OK: false, Reason: "同一触发点已在队列中"}
		}
	}
	if len(q.items) >= q.max {
		return PushResult{OK: false, Reason: "队列已满（等待中的任务太多，先处理或清空）"}
	}
	if it.ID == "" {
		q.seq++
		it.ID = time.Now().Format("150405") + "-" + itoa(int64(q.seq))
	}
	if it.At.IsZero() {
		it.At = time.Now()
	}
	q.items = append(q.items, it)
	if it.Dedup != "" {
		q.seen[it.Dedup] = it.At
		q.order = append(q.order, it.Dedup)
		q.evictSeen()
	}
	q.persistLocked()
	return PushResult{OK: true, ID: it.ID}
}

// evictSeen 只保留最近 256 个去重键，防止长期运行内存缓慢增长。
func (q *TaskQueue) evictSeen() {
	const keep = 256
	for len(q.order) > keep {
		k := q.order[0]
		q.order = q.order[1:]
		delete(q.seen, k)
	}
}

// Pop 取出队首。第二个返回值为 false 表示队列空。
// 约定：pop 出队后“处理完成”必须由调用方调用 Done(dedup) 释放去重键，
// 否则同一触发点不会再入队——这是有意的：正在跑的任务重复到点了
// 不该再排一条进来。调用点：internal/app/v129.go drainQueue（Pop 后 Done）。
// Remove（用户删掉一条排队任务）则会释放去重键，让触发点能再排队。
func (q *TaskQueue) Pop() (TaskItem, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return TaskItem{}, false
	}
	it := q.items[0]
	q.items = q.items[1:]
	q.persistLocked()
	return it, true
}

// releaseDedupLocked 从 seen/order 里摘掉 dedup（调用方需持锁）。
func (q *TaskQueue) releaseDedupLocked(dedup string) {
	if dedup == "" {
		return
	}
	delete(q.seen, dedup)
	for i, k := range q.order {
		if k == dedup {
			q.order = append(q.order[:i], q.order[i+1:]...)
			return
		}
	}
}

// Len 当前等待中的任务数。
func (q *TaskQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// List 返回队列快照（不含 Dedup 键）。
func (q *TaskQueue) List() []TaskItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]TaskItem, len(q.items))
	copy(out, q.items)
	return out
}

// Remove 按 ID 移除，返回是否移除成功。
// 移除同时释放去重键：用户删掉一条排队任务后，同一触发点应该还能再入队。
func (q *TaskQueue) Remove(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, it := range q.items {
		if it.ID == id {
			q.items = append(q.items[:i], q.items[i+1:]...)
			q.releaseDedupLocked(it.Dedup)
			q.persistLocked()
			return true
		}
	}
	return false
}

// Clear 清空队列并释放去重键，返回清掉的条数。
func (q *TaskQueue) Clear() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := len(q.items)
	q.items = nil
	q.seen = map[string]time.Time{}
	q.order = nil
	q.persistLocked()
	return n
}

// Done 标记某个去重键"这一轮已经处理完"，允许下一次同键入队。
func (q *TaskQueue) Done(dedup string) {
	if dedup == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.seen, dedup)
	for i, k := range q.order {
		if k == dedup {
			q.order = append(q.order[:i], q.order[i+1:]...)
			break
		}
	}
}
