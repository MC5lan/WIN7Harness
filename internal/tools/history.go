package tools

// 跨会话检索：项目做到第二天，真正需要的信息往往"上周那次对话里说过"。
// 会话本身一直存在 sessions 目录里，缺的只是一个给模型用的检索入口。
//
// 这里刻意不复用 app 包的会话结构：app 带 windows build tag，而工具层要能
// 在 Linux 上跑单测与自检。做法是只按 JSON 字段名读取必要内容（role/text/tools），
// 结构演进时缺字段就等于空值，不会读崩旧档。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"win7harness/internal/core"
)

func init() {
	register(Tool{
		Name: "history_search",
		Desc: "检索历史会话：在以往所有对话里按关键词查找原话、结论和工具输出。" +
			"适合回答『上次那个报错怎么解决的』『之前定的方案是什么』这类需要跨会话回忆的问题。" +
			"只读，不修改任何文件；返回会话标题、时间与命中片段，需要完整上下文再让用户打开该会话。",
		Schema: `{
  "type":"object",
  "properties":{
    "query":{"type":"string","description":"关键词，支持空格分隔多个词（要求全部命中）"},
    "limit":{"type":"integer","description":"最多返回几条命中，默认 8，最多 30"},
    "days":{"type":"integer","description":"只看最近多少天的会话，0 或留空表示不限"}
  },
  "required":["query"]
}`,
		Run: runHistorySearch,
	})
}

const (
	histDefaultLimit = 8
	histMaxLimit     = 30
	// 单会话解压上限 64MB：再大基本是异常档案。
	histMaxFileBytes = 64 << 20
	// 最多扫描多少个会话文件（按最近修改排序），避免几千个会话全量读盘。
	histMaxFiles = 80
)

// histFile 是会话文件的最小投影。
type histFile struct {
	Conv struct {
		ID    string    `json:"id"`
		Title string    `json:"title"`
		At    time.Time `json:"at"`
		Msgs  []struct {
			Role  string    `json:"role"`
			Text  string    `json:"text"`
			At    time.Time `json:"at"`
			Tools []struct {
				Name   string `json:"name"`
				Args   string `json:"args"`
				Output string `json:"output"`
			} `json:"tools"`
		} `json:"msgs"`
	} `json:"conv"`
}

// histHit 一条命中。
type histHit struct {
	ConvID  string
	Title   string
	At      time.Time
	Role    string
	Around  string
	Ordinal int
}

func runHistorySearch(ctx *Context, args map[string]interface{}) (Result, error) {
	q := strings.TrimSpace(Get(args, "query"))
	if q == "" {
		return Result{}, fmt.Errorf("query 为空")
	}
	limit := GetInt(args, "limit", histDefaultLimit)
	if limit <= 0 {
		limit = histDefaultLimit
	}
	if limit > histMaxLimit {
		limit = histMaxLimit
	}
	days := GetInt(args, "days", 0)

	dir := ctx.HistoryDir
	if dir == "" {
		dir = core.SessionsDir()
	}
	if ctx.Progress != nil {
		ctx.Progress("检索历史会话：" + q)
	}

	hits, scanned, err := SearchHistory(dir, q, limit, days, time.Now())
	if err != nil {
		return Result{}, err
	}
	if len(hits) == 0 {
		return Result{Output: fmt.Sprintf("在最近扫描的 %d 个会话里没有找到「%s」。\n"+
			"可以换更短的关键词（比如只留报错码或文件名），或放宽 days 限制。", scanned, q)}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "关键词：%s\n命中：%d 条（扫描 %d 个会话）\n---\n", q, len(hits), scanned)
	for _, h := range hits {
		title := h.Title
		if title == "" {
			title = "未命名会话"
		}
		fmt.Fprintf(&b, "《%s》%s · 第 %d 条 %s\n", title, h.At.Format("2006-01-02 15:04"), h.Ordinal, roleLabel(h.Role))
		fmt.Fprintf(&b, "  …%s…\n", h.Around)
	}
	b.WriteString("---\n以上是历史对话片段。要用完整上下文，请让用户在会话列表里搜索标题打开该对话。")
	return Result{Output: b.String(), Title: "历史检索"}, nil
}

func roleLabel(r string) string {
	switch r {
	case "user":
		return "用户"
	case "assistant":
		return "助手"
	default:
		if r == "" {
			return "记录"
		}
		return r
	}
}

// SearchHistory 在会话目录里按关键词检索，供工具与 /recall 命令共用。
// 返回命中列表与实际扫描的文件数。查询词按空格切分，要求全部命中（AND）。
func SearchHistory(dir, query string, limit, days int, now time.Time) ([]histHit, int, error) {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil, 0, fmt.Errorf("query 为空")
	}
	files, err := sessionFilesByRecent(dir, histMaxFiles)
	if err != nil {
		return nil, 0, err
	}
	if len(files) == 0 {
		return nil, 0, nil
	}

	var hits []histHit
	scanned := 0
	for _, p := range files {
		hf, err := loadHistFile(p, int64(histMaxFileBytes))
		if err != nil {
			continue
		}
		// 时间窗按"会话内容时间"判定，不按文件修改时间：
		// 拷贝/备份过的老会话 mtime 是新的，用它算会把 40 天前的对话当成本周内容。
		if days > 0 {
			if t := convLatestTime(p, hf); !t.IsZero() && now.Sub(t) > time.Duration(days)*24*time.Hour {
				continue
			}
		}
		scanned++
		title := hf.Conv.Title
		titleHit := matchAll(strings.ToLower(title), words)
		if titleHit && len(hf.Conv.Msgs) == 0 {
			hits = append(hits, histHit{ConvID: hf.Conv.ID, Title: title, At: hf.Conv.At, Role: "", Around: "（标题命中）"})
		}
		for i, m := range hf.Conv.Msgs {
			at := m.At
			if at.IsZero() {
				at = hf.Conv.At
			}
			if text := hitAround(m.Text, words); text != "" {
				hits = append(hits, histHit{ConvID: hf.Conv.ID, Title: title, At: at, Role: m.Role, Around: text, Ordinal: i + 1})
				if len(hits) >= limit {
					return hits, scanned, nil
				}
			}
			for _, t := range m.Tools {
				blob := t.Name + " " + t.Args + " " + t.Output
				if text := hitAround(blob, words); text != "" {
					hits = append(hits, histHit{ConvID: hf.Conv.ID, Title: title, At: at,
						Role: "工具·" + t.Name, Around: text, Ordinal: i + 1})
					if len(hits) >= limit {
						return hits, scanned, nil
					}
				}
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].At.After(hits[j].At) })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, scanned, nil
}

// convLatestTime 取一个会话的"最新内容时间"：优先会话内最后一条消息的时间，
// 退到会话创建时间，都缺失时才用文件修改时间兜底。
func convLatestTime(path string, hf *histFile) time.Time {
	var t time.Time
	if !hf.Conv.At.IsZero() {
		t = hf.Conv.At
	}
	for _, m := range hf.Conv.Msgs {
		if m.At.After(t) {
			t = m.At
		}
	}
	if t.IsZero() {
		if st, err := os.Stat(path); err == nil {
			t = st.ModTime()
		}
	}
	return t
}

// sessionFilesByRecent 返回会话文件列表，按修改时间从新到旧。
// 同时兼容明文 .json 与 gzip 压缩档，跳过备份与临时文件。
func sessionFilesByRecent(dir string, max int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取会话目录失败: %v", err)
	}
	type item struct {
		path string
		mod  time.Time
	}
	var items []item
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".json.gz") {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		items = append(items, item{path: filepath.Join(dir, name), mod: info.ModTime()})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })
	if max > 0 && len(items) > max {
		items = items[:max]
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.path)
	}
	return out, nil
}

// loadHistFile 读取并解析一个会话文件，自动识别 gzip。
// 明文路径也必须先过大小闸：Win7 32 位进程的地址空间只有 2GB，
// 一个异常膨胀的会话（几百 MB）整个读进内存直接 OOM 闪退。
func loadHistFile(path string, maxBytes int64) (*histFile, error) {
	if info, err := os.Stat(path); err == nil && info.Size() > maxBytes {
		return nil, fmt.Errorf("会话文件过大（%s，上限 %s），已跳过：%s", core.HumanSize(info.Size()), core.HumanSize(maxBytes), filepath.Base(path))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		raw, err = core.GunzipData(raw, maxBytes)
		if err != nil {
			return nil, err
		}
	}
	var hf histFile
	if err := json.Unmarshal(raw, &hf); err != nil {
		return nil, err
	}
	return &hf, nil
}

// matchAll 判断文本是否包含全部关键词。
func matchAll(text string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

// hitAround 在文本里定位关键词，返回带上下文的片段；未命中返回空串。
func hitAround(text string, words []string) string {
	if text == "" {
		return ""
	}
	low := strings.ToLower(text)
	if !matchAll(low, words) {
		return ""
	}
	idx := strings.Index(low, words[0])
	if idx < 0 {
		idx = 0
	}
	// 以字符（而非字节）为单位取窗口，避免切断 UTF-8 汉字
	const before, after = 40, 80
	start := idx
	for n := 0; n < before && start > 0; n++ {
		_, size := utf8.DecodeLastRuneInString(text[:start])
		if size == 0 {
			break
		}
		start -= size
	}
	end := idx
	for n := 0; n < after && end < len(text); n++ {
		_, size := utf8.DecodeRuneInString(text[end:])
		if size == 0 {
			break
		}
		end += size
	}
	seg := strings.Join(strings.Fields(text[start:end]), " ")
	if start > 0 {
		seg = "…" + seg
	}
	if end < len(text) {
		seg += "…"
	}
	return seg
}
