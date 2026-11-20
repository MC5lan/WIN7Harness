package core

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 崩溃与断电恢复（轮次日志）。
//
// 会话文件本身是原子写的，掉电最多丢"最后一条消息"。真正会丢的是**任务进度**：
// 模型已经改了一半代码、跑完一半工具，进程没了，重启后用户只看到一堆历史消息，
// 不知道刚才做到哪、该不该接着说"继续"。
//
// 轮次日志就是补这一块的：每次用户发消息时先落一条"开始了"，
// 每完成一个工具更新一次进度，正常收尾时删掉。启动时如果还能看到未收尾的记录，
// 就说明上次是异常退出，据此给用户一条明确的续做提示。
//
// 日志刻意保持了极小的体积（几百字节）并且独立于会话文件：
// 会话文件可能几十 MB，靠它判断"是否中途断电"既慢又容易被压缩格式干扰。

// JournalEntry 是一次"进行中的轮次"。
type JournalEntry struct {
	ConvID    string    `json:"conv_id"`
	Turn      int       `json:"turn"`       // 本次请求内的工具循环轮次
	UserText  string    `json:"user_text"`  // 用户当时的输入（截断保存）
	StartedAt time.Time `json:"started_at"` // 开始时间
	UpdatedAt time.Time `json:"updated_at"` // 最近一次进度更新
	Model     string    `json:"model,omitempty"`
	WorkDir   string    `json:"work_dir,omitempty"`
	// LastTool 最近一次完成的工具，恢复提示里会带上，帮用户回忆做到哪一步。
	LastTool string `json:"last_tool,omitempty"`
}

// Journal 管理轮次日志目录。
type Journal struct {
	dir string
	mu  sync.Mutex
}

// journalExt 日志后缀。故意不含 .json，避免被会话装载逻辑误当成会话文件。
const journalExt = ".journal"

// NewJournal 创建日志管理器，dir 通常是 <data>/sessions。
func NewJournal(dir string) *Journal { return &Journal{dir: dir} }

// Dir 返回日志目录。
func (j *Journal) Dir() string { return j.dir }

// journalPath 返回某会话的日志路径。
func (j *Journal) journalPath(convID string) string {
	return filepath.Join(j.dir, sanitizeID(convID)+journalExt)
}

// sanitizeID 只允许十六进制会话 ID（会话 ID 本来就是 %016x 形式），
// 其它值整体十六进制化——此前给非 hex 字符替换成 '_'，
// 会让不同的输入映射到同一个文件名（"a/b" 与 "a_b" 碰撞）。
func sanitizeID(id string) string {
	if id == "" {
		return "unknown"
	}
	allHex := true
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			allHex = false
		}
		if !allHex {
			break
		}
	}
	if allHex {
		return id
	}
	return "hex-" + hex.EncodeToString([]byte(id))
}

// Begin 记录一次轮次开始。写入前会把用户输入截断到 200 字符：
// 提示只需要帮用户回忆"我在干什么"，不需要存整段粘贴的日志。
func (j *Journal) Begin(e JournalEntry) error {
	if e.ConvID == "" {
		return fmt.Errorf("缺少会话 ID")
	}
	now := time.Now()
	if e.StartedAt.IsZero() {
		e.StartedAt = now
	}
	e.UpdatedAt = now
	e.UserText = clipRunes(e.UserText, 200)
	return j.write(e.ConvID, e)
}

// Progress 更新进度（第几轮、最后完成的工具）。写入失败不影响主流程，
// 由调用方决定是否记日志——恢复能力是增强，不能反过来阻断正常对话。
// read-modify-write 全程持同一把锁：否则 Finish 刚删掉的已收尾条目
// 会被这里的写复活，下次启动 Pending() 报"上一轮没收尾"的假象。
func (j *Journal) Progress(convID string, round int, lastTool string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	e, err := j.readLocked(convID)
	if err != nil {
		// 日志不在了（正常收尾后被删）就重新起一条，保证后续还能恢复。
		e = &JournalEntry{ConvID: convID, StartedAt: time.Now()}
	}
	e.Turn = round
	if lastTool != "" {
		e.LastTool = lastTool
	}
	e.UpdatedAt = time.Now()
	return j.writeLocked(convID, *e)
}

// Finish 删除日志，表示这一轮正常结束（含用户主动停止）。
func (j *Journal) Finish(convID string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	err := os.Remove(j.journalPath(convID))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// readLocked 读取单条日志（调用方必须持 j.mu）。
func (j *Journal) readLocked(convID string) (*JournalEntry, error) {
	data, err := os.ReadFile(j.journalPath(convID))
	if err != nil {
		return nil, err
	}
	var e JournalEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// write 原子写日志（.tmp + rename），避免掉电留下半条 JSON。
func (j *Journal) write(convID string, e JournalEntry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.writeLocked(convID, e)
}

// writeLocked 原子写日志（调用方必须持 j.mu）。
// tmp 用进程+纳秒命名：进程在 rename 前崩溃，
// 不会留下看起来像正规 .tmp 的僵尸文件干扰清理。
func (j *Journal) writeLocked(convID string, e JournalEntry) error {
	if j.dir == "" {
		return fmt.Errorf("未配置日志目录")
	}
	if err := os.MkdirAll(j.dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	p := j.journalPath(convID)
	tmp := p + ".tmp." + time.Now().Format("150405.000000")
	if err := WriteFileAtomic(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Pending 返回所有未收尾的轮次，按开始时间倒序（最近中断的排在前面）。
// 解析失败或明显过期的（超过 7 天）记录会被顺手清掉，避免日志目录越积越多。
func (j *Journal) Pending() ([]JournalEntry, error) {
	ents, err := os.ReadDir(j.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []JournalEntry
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), journalExt) {
			continue
		}
		p := filepath.Join(j.dir, ent.Name())
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			continue
		}
		var e JournalEntry
		if jerr := json.Unmarshal(data, &e); jerr != nil {
			// 半截 JSON：说明断电正好卡在写日志这里，直接丢弃。
			os.Remove(p)
			continue
		}
		if e.StartedAt.IsZero() {
			e.StartedAt = e.UpdatedAt
		}
		if time.Since(e.StartedAt) > 7*24*time.Hour {
			os.Remove(p)
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].StartedAt.After(out[b].StartedAt) })
	return out, nil
}

// RecoveryNotice 生成给用户看的恢复提示。
// 措辞要求：说清"哪一轮断了、做到哪一步、下一步该干什么"，
// 只说"检测到异常退出"对用户没有价值。
func RecoveryNotice(e JournalEntry) string {
	var b strings.Builder
	b.WriteString("上次运行在这一轮中断了（崩溃或断电）")
	if !e.StartedAt.IsZero() {
		b.WriteString("，时间 ")
		b.WriteString(e.StartedAt.Format("01-02 15:04"))
	}
	b.WriteString("。\n")
	if t := strings.TrimSpace(e.UserText); t != "" {
		b.WriteString("当时的任务：")
		b.WriteString(clipRunes(t, 80))
		b.WriteString("\n")
	}
	if e.Turn > 0 {
		fmt.Fprintf(&b, "已进行到第 %d 轮工具调用", e.Turn)
		if e.LastTool != "" {
			b.WriteString("，最后一个完成的工具是 ")
			b.WriteString(e.LastTool)
		}
		b.WriteString("。\n")
	}
	b.WriteString("已完成的改动与工具结果都保留在会话里。输入 /resume 让我接着做，或直接补充新的要求。")
	return b.String()
}

// RecoveryPrompt 是 /resume 时回灌给模型的指令。
// 写清楚"先核对现状再继续"，避免模型在不知情的情况下重复改同一处代码。
func RecoveryPrompt(e JournalEntry) string {
	var b strings.Builder
	b.WriteString("【恢复中断的任务】上次运行在完成任务的过程中异常退出（崩溃或断电），进程没有正常收尾。\n")
	if t := strings.TrimSpace(e.UserText); t != "" {
		b.WriteString("中断时的任务：")
		b.WriteString(clipRunes(t, 200))
		b.WriteString("\n")
	}
	if e.Turn > 0 {
		fmt.Fprintf(&b, "中断前进度：第 %d 轮工具调用", e.Turn)
		if e.LastTool != "" {
			b.WriteString("，最后完成的工具是 ")
			b.WriteString(e.LastTool)
		}
		b.WriteString("。\n")
	}
	b.WriteString("请先核对当前代码与文件的真实状态（读文件、看改动），确认哪些已经完成、哪些还差，")
	b.WriteString("再从中断处继续，不要重复已经成功的修改。完成后按常规跑一次验证。")
	return b.String()
}

// clipRunes 按字符截断，保证中文不被截半。
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
