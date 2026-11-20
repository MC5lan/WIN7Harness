// 会话索引与增量读取。
//
// 为什么放在 core 而不是 app：会话文件结构是"磁盘契约"，
// 手机端（远程 API）、/find、以及自检都需要读它；
// 放在 app（windows tag 包）里就没法在 Linux 上跑自检。
package core

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SessionBrief 会话摘要，够手机端列表显示。
type SessionBrief struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	At      time.Time `json:"at"`
	Msgs    int       `json:"msgs"`
	Preview string    `json:"preview,omitempty"`
	Size    int64     `json:"size"`
	Pinned  bool      `json:"pinned,omitempty"`
}

// SessionLine 会话里的一行，手机端按行追加。
// 只带文本：工具参数与输出动辄几万字，手机上没法看，也没必要传。
type SessionLine struct {
	Role string    `json:"role"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
	Tool string    `json:"tool,omitempty"` // 有值表示这一轮调了工具（给出名字即可）
	Err  bool      `json:"err,omitempty"`
}

// validSessionID 只接受十六进制会话 ID。
// 手机端传来的 id 会拼进文件路径，不校验就等于把任意文件读取的钥匙递出去。
func validSessionID(id string) bool {
	if len(id) == 0 || len(id) > 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// readSessionScan 打开会话文件并以流式方式扫描（gzip 自动识别、.bak 回退）。
// keepFrom<0 表示不收集消息正文（只出摘要）；>=0 时保留 [keepFrom, ∞) 中最后 keepMax 条。
// 返回的 size 为磁盘文件字节数。
func readSessionScan(dir, id string, keepFrom, keepMax int) (*sessScanResult, int64, error) {
	if !validSessionID(id) {
		return nil, 0, errors.New("会话 ID 不合法")
	}
	for _, ext := range []string{".json.gz", ".json", ".json.bak"} {
		p := filepath.Join(dir, id+ext)
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		// 嗅探 gzip 魔数（只读前 2 字节，再回头从头读）
		magic := make([]byte, 2)
		_, merr := io.ReadFull(f, magic)
		_, _ = f.Seek(0, io.SeekStart)
		var rc io.ReadCloser = f
		if merr == nil && magic[0] == 0x1f && magic[1] == 0x8b {
			zr, zerr := gzip.NewReader(f)
			if zerr != nil {
				// 声明 gzip 却解不开：记日志并让下一个候选接上，别拿压缩字节去 json 解析
				_ = f.Close()
				Logf("会话 %s%s 疑似损坏（gzip 头：%v），尝试下一个候选文件", id, ext, zerr)
				continue
			}
			rc = &capReadCloser{r: zr, u: multiCloser{zr, f}, max: maxDecompressedBytes}
		}
		res, perr := scanSessionLite(rc, keepFrom, keepMax)
		_ = rc.Close()
		if perr != nil {
			Logf("会话 %s%s 解析失败：%v（尝试下一个候选文件）", id, ext, perr)
			continue
		}
		return res, st.Size(), nil
	}
	return nil, 0, os.ErrNotExist
}

// previewText 取最后一条有文本的消息，压成一行。
func previewText(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	t = strings.ReplaceAll(t, "\r", " ")
	t = strings.ReplaceAll(t, "\n", " ")
	r := []rune(t)
	if len(r) > 48 {
		return string(r[:48]) + "…"
	}
	return string(r)
}

// SessionIndex 扫描会话目录，按最近活跃倒序返回摘要。
// limit <= 0 表示不限。解析失败的单条会话跳过而不是整个失败：
// 一个坏文件不该让手机端整个会话列表空掉。
// 手机每 10 秒刷一次列表——不能每次都把目录全解析一遍：
// 先按 mtime 倒序截出最新的候选，再流式逐个解析，最后按解析出的时间再排一次。
// 流式解析保证内存占用与会话文件大小无关（长对话也只用 O(窗口) 内存）。
//
// v1.31.1 起增加“会话索引缓存” sessions/.sessions-index.json：
// 目录项 mtime+size 未变化的会话直接复用上次摘要，不再读取正文。
// 启动时成本从 O(全部会话字节) 降到 O(目录项数量)，大历史库能秒开。

const sessionIndexCacheVersion = 1

type sessionIndexCacheEntry struct {
	Mtime int64        `json:"mtime"`
	Size  int64        `json:"size"`
	Brief SessionBrief `json:"brief"`
}

type sessionIndexCacheFile struct {
	V       int                               `json:"v"`
	Entries map[string]sessionIndexCacheEntry `json:"entries"`
}

var sessionIndexCacheMu sync.Mutex

func sessionIndexCachePath(dir string) string {
	return filepath.Join(dir, ".sessions-index.json")
}

func loadSessionIndexCache(dir string) map[string]sessionIndexCacheEntry {
	raw, err := os.ReadFile(sessionIndexCachePath(dir))
	if err != nil {
		return nil
	}
	var cf sessionIndexCacheFile
	if json.Unmarshal(raw, &cf) != nil || cf.V != sessionIndexCacheVersion || cf.Entries == nil {
		return nil
	}
	return cf.Entries
}

func saveSessionIndexCache(dir string, entries map[string]sessionIndexCacheEntry) {
	if len(entries) == 0 {
		return
	}
	b, err := json.Marshal(sessionIndexCacheFile{V: sessionIndexCacheVersion, Entries: entries})
	if err != nil {
		return
	}
	// 缓存是加速层，写失败不影响列表；用原子写避免半截 JSON。
	if err := WriteFileAtomic(sessionIndexCachePath(dir), b, 0o600); err != nil {
		Logf("会话索引缓存写入失败：%v", err)
	}
}

func SessionIndex(dir string, limit int) ([]SessionBrief, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	type cand struct {
		id    string
		mtime time.Time
		size  int64
	}
	cands := make([]cand, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		var id string
		switch {
		case strings.HasSuffix(name, ".json.gz"):
			id = strings.TrimSuffix(name, ".json.gz")
		case strings.HasSuffix(name, ".json"):
			id = strings.TrimSuffix(name, ".json")
		default:
			continue
		}
		if !validSessionID(id) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		cands = append(cands, cand{id, info.ModTime(), info.Size()})
	}
	// 预选：上限兜底 4 倍，坏文件再多也不会把列表拖垮
	pre := limit
	if pre <= 0 {
		pre = 30
	}
	if pre > len(cands) {
		pre = len(cands)
	}
	keep := pre * 4
	if keep > len(cands) {
		keep = len(cands)
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mtime.After(cands[j].mtime) })
	allIDs := make(map[string]bool, len(cands))
	for _, cd := range cands {
		allIDs[cd.id] = true
	}
	if keep > 0 {
		cands = cands[:keep]
	}

	cache := loadSessionIndexCache(dir)
	if cache == nil {
		cache = map[string]sessionIndexCacheEntry{}
	}
	cacheChanged := false
	out := make([]SessionBrief, 0, len(cands))
	for _, cd := range cands {
		if ce, ok := cache[cd.id]; ok && ce.Mtime == cd.mtime.UnixNano() && ce.Size == cd.size {
			b := ce.Brief
			b.ID = cd.id
			b.Size = cd.size
			out = append(out, b)
			continue
		}
		res, size, err := readSessionScan(dir, cd.id, -1, 0)
		if err != nil {
			continue
		}
		at := res.At
		if at.IsZero() {
			at = cd.mtime
		}
		title := res.Title
		if strings.TrimSpace(title) == "" {
			title = "未命名会话"
		}
		brief := SessionBrief{
			ID:      cd.id,
			Title:   title,
			At:      at,
			Msgs:    res.Msgs,
			Preview: previewText(res.LastText),
			Size:    size,
			Pinned:  res.Pinned,
		}
		out = append(out, brief)
		cache[cd.id] = sessionIndexCacheEntry{
			Mtime: cd.mtime.UnixNano(), Size: cd.size, Brief: brief,
		}
		cacheChanged = true
	}
	// 已删除的会话从缓存里摘掉，防止缓存无限增长。
	for id := range cache {
		if !allIDs[id] {
			delete(cache, id)
			cacheChanged = true
		}
	}
	if cacheChanged {
		sessionIndexCacheMu.Lock()
		saveSessionIndexCache(dir, cache)
		sessionIndexCacheMu.Unlock()
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SessionLines 返回会话里第 from 条开始的至多 max 条消息，total 为总条数。
// from 传上次拿到的 total 即为增量；total 变小（会话被截断/新建）时调用方应重新拉全量。
// 只用流式解析：从第 from 条起收集，最多保留最后 max 条，内存与文件大小无关。
func SessionLines(dir, id string, from, max int) ([]SessionLine, int, error) {
	if from < 0 {
		from = 0
	}
	if max <= 0 || max > 200 {
		max = 60
	}
	// 窗口：保留 [from, ∞) 中最后 max 条。
	// （from 若超过总条数，scan 会收集到空切片，total 也如实返回，与旧行为一致。）
	res, _, err := readSessionScan(dir, id, from, max)
	if err != nil {
		return nil, 0, err
	}
	total := res.Msgs
	out := make([]SessionLine, 0, len(res.Collected))
	for _, m := range res.Collected {
		ln := SessionLine{Role: m.Role, Text: m.Text, At: m.At, Err: m.Err != ""}
		if ln.Text == "" && len(m.Tools) > 0 {
			names := make([]string, 0, len(m.Tools))
			for _, t := range m.Tools {
				if t.Name != "" {
					names = append(names, t.Name)
				}
			}
			if len(names) > 0 {
				ln.Tool = strings.Join(names, ",")
			}
		}
		if strings.TrimSpace(ln.Text) == "" && ln.Tool == "" && !ln.Err {
			continue
		}
		out = append(out, ln)
	}
	return out, total, nil
}
