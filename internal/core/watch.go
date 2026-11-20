package core

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// —— 变化触发守护 ——
//
// 场景：改完代码想让 harness 立刻跑编译/测试，但不想手动敲一遍命令。
// 做法：定时对目录做轻量快照（路径 + 大小 + 修改时间），有变化就投一条提示词。
//
// 为什么不用 ReadDirectoryChangesW：那个 API 对深层目录要递归注册句柄，
// 大仓库一次能挂上千个句柄，还容易在 U 盘、网络盘上直接失败；轮询 3 秒一次的代价
// 是几百毫秒的遍历，用户完全无感，而失败模式简单得多（遍历不到就是没变化）。
// 只在状态有差异时才读文件，硬盘不会被无谓扫醒。

// WatchRule 一条守护规则。
type WatchRule struct {
	ID          string   `json:"id"`
	Path        string   `json:"path"`
	Prompt      string   `json:"prompt"`
	Patterns    []string `json:"patterns,omitempty"` // 空表示全部文件；支持 *.go、src/* 这类
	Ignore      []string `json:"ignore,omitempty"`   // 目录名或路径片段
	DebounceSec int      `json:"debounce_sec,omitempty"`
	// MinGapSec（v1.28）两次触发之间的最小间隔，默认 30 秒。
	// Debounce 管的是"一次改动还没停下来"，MinGap 管的是"刚跑完又改下一处"：
	// 连续改十几分钟时，没有它就会变成每隔几秒叫一次模型，钱和注意力都烧得厉害。
	MinGapSec int       `json:"min_gap_sec,omitempty"`
	Enabled   bool      `json:"enabled"`
	Fires     int       `json:"fires"`
	LastFire  time.Time `json:"last_fire,omitempty"`
	Created   time.Time `json:"created,omitempty"`

	// 静默窗口用：还没到点的合并变更。只在内存里，不落盘——
	// 进程重启后基线快照已经是最新的，旧的待触发变更再放出来只会误报。
	pending   DiffResult
	pendingAt time.Time
}

// FileState 一个文件的可比状态。修改时间用纳秒级 Unix 值，
// 只比到秒会把"同一秒内改两次"吞掉。
type FileState struct {
	Size int64 `json:"size"`
	Mod  int64 `json:"mod"`
}

// Snapshot 相对路径 → 状态。
type Snapshot map[string]FileState

var defaultIgnoreDirs = []string{
	".git", ".svn", ".hg", "node_modules", "vendor", "dist", "build",
	"__pycache__", ".idea", ".vscode", ".cache", "target", "bin", "obj",
}

// MaxSnapshotFiles 单次快照的文件数上限。大仓库（几十万文件）遍历一遍要几秒，
// 与其让守护线程拖慢界面，不如明确截断：守护是"及时发现改动"，不是"保证一个不漏"。
const MaxSnapshotFiles = 20000

// TakeSnapshot 遍历 root 生成快照。patterns 为空表示所有文件。
func TakeSnapshot(root string, patterns, ignore []string, limit int) (Snapshot, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("未指定目录")
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("守护对象必须是目录：" + root)
	}
	if limit <= 0 {
		limit = MaxSnapshotFiles
	}
	ign := map[string]bool{}
	for _, d := range append(append([]string{}, defaultIgnoreDirs...), ignore...) {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			ign[d] = true
		}
	}

	snap := Snapshot{}
	stopped := errors.New("stop")
	err = filepath.Walk(root, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil // 单个目录不可读不该中断整次快照
		}
		if fi.IsDir() {
			if path != root && ign[strings.ToLower(fi.Name())] {
				return filepath.SkipDir
			}
			return nil
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if len(patterns) > 0 && !MatchWatchPattern(rel, patterns) {
			return nil
		}
		snap[rel] = FileState{Size: fi.Size(), Mod: fi.ModTime().UnixNano()}
		if len(snap) >= limit {
			return stopped
		}
		return nil
	})
	if err != nil && !errors.Is(err, stopped) {
		return nil, err
	}
	return snap, nil
}

// MatchWatchPattern 判断相对路径是否命中规则。
// 支持 glob 语义：
//
//	**/<seg>     任意层级匹配该段（含 0 层）
//	<seg>/**     该前缀下全部内容
//	*.go         单层通配（不跨 /）
//	src/**.go    等价于 src/**/*.go，即**可在任意深度
//
// 此前 `**/*.go` 走 filepath.Match 永不命中，`src/**` 退化成
// 前缀 src（连 srcfoo/x.go 都命中）——写规则的人会以为自己守住了，其实没有。
func MatchWatchPattern(rel string, patterns []string) bool {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	for _, p := range patterns {
		p = strings.TrimSpace(filepath.ToSlash(p))
		if p == "" {
			continue
		}
		// "**.go" 吸收任意层级后按 *.go 匹配末段：src/**.go ≡ src/**/*.go
		p = strings.ReplaceAll(p, "/**.", "/**/*.")
		if strings.HasPrefix(p, "**.") {
			p = "**/*." + p[3:]
		}
		if p == "*" || p == "**" {
			return true
		}
		if !strings.Contains(p, "/") {
			// 无斜杠的模式：只匹配文件名（"*.go" 这类）
			if ok, _ := filepath.Match(p, base); ok {
				return true
			}
			if !strings.ContainsAny(p, "*?") && strings.HasSuffix(base, "."+p) {
				return true
			}
			continue
		}
		if matchGlobSegments(strings.Split(rel, "/"), strings.Split(p, "/")) {
			return true
		}
	}
	return false
}

// matchGlobSegments 按段匹配：** 可吸收任意段数（含 0），* 吸收单段内任意字符。
func matchGlobSegments(rel, pat []string) bool {
	ri, pi := 0, 0
	for pi < len(pat) {
		if pat[pi] == "**" {
			// 末尾的 **：剩余全部命中
			if pi == len(pat)-1 {
				return true
			}
			for k := ri; k <= len(rel); k++ {
				if matchGlobSegments(rel[k:], pat[pi+1:]) {
					return true
				}
			}
			return false
		}
		if ri >= len(rel) {
			return false
		}
		if ok, _ := filepath.Match(pat[pi], rel[ri]); !ok {
			return false
		}
		ri++
		pi++
	}
	return ri == len(rel)
}

// DiffResult 一次比较的结果。
type DiffResult struct {
	Added   []string
	Changed []string
	Removed []string
}

// Total 变更文件总数。
func (d DiffResult) Total() int { return len(d.Added) + len(d.Changed) + len(d.Removed) }

// Empty 是否没有变化。
func (d DiffResult) Empty() bool { return d.Total() == 0 }

// DiffSnapshot 比较两次快照。结果排序，保证界面与日志可读、可复现。
func DiffSnapshot(old, next Snapshot) DiffResult {
	var d DiffResult
	for p, st := range next {
		if oldState, ok := old[p]; !ok {
			d.Added = append(d.Added, p)
		} else if oldState != st {
			d.Changed = append(d.Changed, p)
		}
	}
	for p := range old {
		if _, ok := next[p]; !ok {
			d.Removed = append(d.Removed, p)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Changed)
	sort.Strings(d.Removed)
	return d
}

// ShouldFire 防抖判定：变化后要等目录安静下来再触发，
// 否则一次保存（编辑器先写临时文件再改名）会连发三四条提示词。
func ShouldFire(lastFire, now time.Time, debounce time.Duration) bool {
	if debounce <= 0 {
		debounce = 3 * time.Second
	}
	return now.Sub(lastFire) >= debounce
}

// Describe 给界面用的一行说明。
func (w *WatchRule) Describe() string {
	when := "任何文件"
	if len(w.Patterns) > 0 {
		when = strings.Join(w.Patterns, "/")
	}
	state := "守护中"
	if !w.Enabled {
		state = "已暂停"
	}
	return w.ID + " · " + w.Path + " · " + when + " · " + state + " · 已触发 " + watchNum(w.Fires) + " 次"
}

func watchNum(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// —— 持久化 ——

// WatchesPath 守护规则的落盘位置。
func WatchesPath() string { return filepath.Join(DataDir(), "watches.json") }

// WatchStore 守护规则清单。
type WatchStore struct {
	Path  string       `json:"-"`
	Items []*WatchRule `json:"items"`
	Seq   int          `json:"seq"`
	// Snapshots 各规则的上一份快照。跟着规则一起落盘，
	// 重启后第一次比较才不会把整个仓库判成"全部新增"。
	Snapshots map[string]Snapshot `json:"snapshots,omitempty"`
	// LoadErr（v1.29）同上：规则文件坏了要留证据 + 告诉用户。
	LoadErr string `json:"-"`

	// mu 保护 Items/Snapshots 与 JSON 序列化：
	// Save（UI 线程）和 Poll（轮询 goroutine）同时碰 map 就是 concurrent map access，
	// 那是 Go 直接 fatal、不可 recover。
	mu sync.Mutex

	// lastSnapSave 轮询推进基线后的落盘节流：
	// 只推内存不落盘，重启后第一轮会把一周的改动判成“全部新增”；
	// 全量写又太沉，所以按时间节流而不是每次推进都写。
	lastSnapSave time.Time
}

// LoadWatches 读取守护清单；损坏时返回空清单并把原因记在 LoadErr。
func LoadWatches(path string) *WatchStore {
	if path == "" {
		return &WatchStore{Path: path}
	}
	var loaded WatchStore
	found, loadErr := loadStoreFile(path, "守护规则损坏，已备份为 %s，请重新登记", &loaded)
	if !found {
		st := &WatchStore{Path: path}
		st.LoadErr = loadErr
		return st
	}
	loaded.Path = path
	if loaded.Snapshots == nil {
		loaded.Snapshots = map[string]Snapshot{}
	}
	return &loaded
}

// Save 原子写入（MkdirAll + WriteFileAtomic，0600）。
func (st *WatchStore) Save() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.saveLocked()
}

// saveLocked 已持锁版本：Marshal 必须在锁内做（Items/Snapshots 不撕裂）。
func (st *WatchStore) saveLocked() error {
	return writeStoreAtomic(st.Path, st)
}

// Add 追加一条守护规则。
func (st *WatchStore) Add(path, prompt string, patterns, ignore []string, now time.Time) (*WatchRule, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	abs := strings.TrimSpace(path)
	if abs == "" {
		return nil, errors.New("未指定目录")
	}
	if a, err := filepath.Abs(abs); err == nil {
		abs = a
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, errors.New("目录不可读：" + abs)
	}
	if !info.IsDir() {
		return nil, errors.New("不是目录：" + abs)
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("缺少要执行的提示词")
	}
	if st.Snapshots == nil {
		st.Snapshots = map[string]Snapshot{}
	}
	st.Seq++
	w := &WatchRule{
		ID:          "watch-" + watchNum(st.Seq),
		Path:        abs,
		Prompt:      strings.TrimSpace(prompt),
		Patterns:    patterns,
		Ignore:      ignore,
		DebounceSec: 3,
		MinGapSec:   30,
		Enabled:     true,
		Created:     now,
	}
	// 建规则时先拍一张基线快照，避免第一次轮询就"全新增"触发。
	if snap, e := TakeSnapshot(abs, patterns, ignore, 0); e == nil {
		st.Snapshots[w.ID] = snap
	}
	st.Items = append(st.Items, w)
	if err := st.saveLocked(); err != nil {
		st.Items = st.Items[:len(st.Items)-1]
		delete(st.Snapshots, w.ID)
		return nil, err
	}
	return w, nil
}

// Remove 删除一条规则。
func (st *WatchStore) Remove(id string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i, it := range st.Items {
		if strings.EqualFold(it.ID, id) {
			st.Items = append(st.Items[:i], st.Items[i+1:]...)
			delete(st.Snapshots, it.ID)
			_ = st.saveLocked()
			return true
		}
	}
	return false
}

// SetEnabled 暂停/恢复一条规则；恢复时重拍基线，免得把暂停期间的变化一次性倒出来。
func (st *WatchStore) SetEnabled(id string, on bool, now time.Time) (*WatchRule, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, it := range st.Items {
		if strings.EqualFold(it.ID, id) {
			it.Enabled = on
			if on {
				if snap, e := TakeSnapshot(it.Path, it.Patterns, it.Ignore, 0); e == nil {
					if st.Snapshots == nil {
						st.Snapshots = map[string]Snapshot{}
					}
					st.Snapshots[it.ID] = snap
				}
			}
			_ = st.saveLocked()
			return it, true
		}
	}
	return nil, false
}

// Poll 对一条规则做一次轮询，返回是否触发与（合并后的）变更明细。
//
// 语义是"静默窗口"而不是简单的限流：
// 拷贝一个目录时文件会连着变几十秒，如果一有变化就触发，模型会在文件写到一半时
// 抢着去读，读到半截文件；旧的 ShouldFire 做法更糟——窗口内的变化直接丢弃，
// 用户看到的是"我明明放了文件它却不动"。
// 所以这里改成：变化先并进 pending，等连续 debounce 秒没有新变化再一次性触发。
func (st *WatchStore) Poll(rule *WatchRule, now time.Time) (bool, DiffResult) {
	if rule == nil || !rule.Enabled {
		return false, DiffResult{}
	}
	// 快照本身可能很慢（几万个文件）：不放锁里扫文件系统，
	// 锁住的只是 map/规则字段的读改。
	snap, err := TakeSnapshot(rule.Path, rule.Patterns, rule.Ignore, 0)
	if err != nil {
		return false, DiffResult{}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.Snapshots == nil {
		st.Snapshots = map[string]Snapshot{}
	}
	prev := st.Snapshots[rule.ID]
	if prev == nil {
		st.Snapshots[rule.ID] = snap
		return false, DiffResult{}
	}
	diff := DiffSnapshot(prev, snap)
	if !diff.Empty() {
		st.Snapshots[rule.ID] = snap // 推进基线，同一批变化不会重复累加
		rule.pending = rule.pending.merge(diff)
		rule.pendingAt = now
		// 节流落盘：基线跟上磁盘，重启后不会爆炸式误报“全新增”。
		if now.Sub(st.lastSnapSave) > 30*time.Second {
			st.lastSnapSave = now
			_ = st.saveLocked()
		}
	}
	if rule.pendingAt.IsZero() || rule.pending.Empty() {
		return false, DiffResult{}
	}
	debounce := time.Duration(rule.DebounceSec) * time.Second
	if debounce < 0 {
		debounce = 0
	}
	if now.Sub(rule.pendingAt) < debounce {
		return false, DiffResult{} // 还在变，继续等下一次静默
	}
	// 静默窗口：上一轮刚触发过就继续攒着，攒到窗口结束再一起报。
	// 变更不丢——pendingAt 不推进，pending 继续合并。
	gap := time.Duration(rule.MinGapSec) * time.Second
	if gap > 0 && !rule.LastFire.IsZero() && now.Sub(rule.LastFire) < gap {
		return false, DiffResult{}
	}
	out := rule.pending
	rule.pending = DiffResult{}
	rule.pendingAt = time.Time{}
	rule.LastFire = now
	rule.Fires++
	return true, out
}

// merge 合并两批变更。
// 同一个文件可能"先新增又修改"或"改了又删"，只该报最重的那一档：
// 新增 > 修改 > 删除，删掉的不再报新增/修改。
func (d DiffResult) merge(o DiffResult) DiffResult {
	union := func(a, b []string) []string {
		seen := map[string]bool{}
		out := make([]string, 0, len(a)+len(b))
		for _, s := range a {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
		for _, s := range b {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
		return out
	}
	added := union(d.Added, o.Added)
	changed := make([]string, 0, len(d.Changed)+len(o.Changed))
	inAdded := map[string]bool{}
	for _, s := range added {
		inAdded[s] = true
	}
	for _, s := range union(d.Changed, o.Changed) {
		if !inAdded[s] {
			changed = append(changed, s)
		}
	}
	inChanged := map[string]bool{}
	for _, s := range changed {
		inChanged[s] = true
	}
	removed := make([]string, 0, len(d.Removed)+len(o.Removed))
	for _, s := range union(d.Removed, o.Removed) {
		if !inAdded[s] && !inChanged[s] {
			removed = append(removed, s)
		}
	}
	return DiffResult{Added: added, Changed: changed, Removed: removed}
}

// ChangeSummary 把变更明细压成一行，塞进提示词里给模型看。
func (d DiffResult) ChangeSummary() string {
	parts := make([]string, 0, 3)
	add := func(label string, list []string) {
		if len(list) == 0 {
			return
		}
		show := list
		more := ""
		if len(show) > 10 {
			show = show[:10]
			more = " 等 " + watchNum(len(list)) + " 个"
		}
		parts = append(parts, label+"："+strings.Join(show, ", ")+more)
	}
	add("新增", d.Added)
	add("修改", d.Changed)
	add("删除", d.Removed)
	return strings.Join(parts, "；")
}
