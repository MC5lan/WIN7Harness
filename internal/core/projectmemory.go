package core

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 项目长期记忆：<工作区>/.harness/memory.md。
//
// 与 AGENTS.md 的分工：
//   - AGENTS.md 是"规矩"（人写的约定，改得少）；
//   - memory.md 是"事实"（跑任务时踩出来的坑、确认过的外部信息，会一直长）。
//
// 故意用纯文本单行条目：能直接 git diff、能人工编辑，也不需要任何解析依赖。
// 每行形如：- 2026-11-14 [构建] Go 1.20 交叉编译 386 时需要 GO386=sse2

// MemEntry 一条项目记忆。
type MemEntry struct {
	At   string
	Tag  string
	Text string
}

// MemoryFile 返回项目记忆文件路径。
func MemoryFile(workDir string) string {
	if strings.TrimSpace(workDir) == "" {
		return ""
	}
	return filepath.Join(workDir, ".harness", "memory.md")
}

var memCache struct {
	sync.Mutex
	path  string
	mtime time.Time
	size  int64
	text  string
}

// readMemoryText 带 mtime 缓存地读取文件：每轮都拼系统提示，不能每次真读盘。
func readMemoryText(path string) string {
	if path == "" {
		return ""
	}
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	memCache.Lock()
	defer memCache.Unlock()
	if memCache.path == path && memCache.mtime.Equal(st.ModTime()) && memCache.size == st.Size() {
		return memCache.text
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	memCache.path, memCache.mtime, memCache.size = path, st.ModTime(), st.Size()
	memCache.text = string(data)
	return memCache.text
}

// LoadMemory 解析全部条目（新的在前）。
func LoadMemory(workDir string) []MemEntry {
	text := readMemoryText(MemoryFile(workDir))
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []MemEntry
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		e := parseMemLine(strings.TrimSpace(line[2:]))
		if e.Text != "" {
			out = append(out, e)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func parseMemLine(s string) MemEntry {
	var e MemEntry
	if len(s) > 10 && s[4] == '-' && s[7] == '-' {
		e.At = s[:10]
		s = strings.TrimSpace(s[10:])
	}
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			e.Tag = s[1:i]
			s = strings.TrimSpace(s[i+1:])
		}
	}
	e.Text = s
	return e
}

// AppendMemory 追加一条记忆；文本内的换行会被压成空格，保证一行一条。
func AppendMemory(workDir, tag, text string) (string, error) {
	path := MemoryFile(workDir)
	if path == "" {
		return "", fmt.Errorf("未设置工作区，无法保存项目记忆")
	}
	text = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " "))
	if text == "" {
		return "", fmt.Errorf("记忆内容为空")
	}
	if len([]rune(text)) > 500 {
		text = string([]rune(text)[:500])
	}
	tag = strings.TrimSpace(strings.ReplaceAll(tag, "]", " "))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	line := fmt.Sprintf("- %s [%s] %s\n", time.Now().Format("2006-01-02"), tag, text)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(line); err != nil {
		return "", err
	}
	return line, nil
}

// SearchMemory 按关键词检索（任一关键词命中即算），空关键词返回全部。
func SearchMemory(workDir string, keywords []string, limit int) []MemEntry {
	all := LoadMemory(workDir)
	if len(keywords) == 0 {
		if limit > 0 && len(all) > limit {
			return all[:limit]
		}
		return all
	}
	var out []MemEntry
	for _, e := range all {
		low := strings.ToLower(e.Text + " " + e.Tag)
		for _, k := range keywords {
			k = strings.ToLower(strings.TrimSpace(k))
			if k != "" && strings.Contains(low, k) {
				out = append(out, e)
				break
			}
		}
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// MemoryDigest 生成注入系统提示的摘要：取最近 limit 条，总长受限。
func MemoryDigest(workDir string, limit, maxChars int) string {
	all := LoadMemory(workDir)
	if len(all) == 0 {
		return ""
	}
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	var b strings.Builder
	for _, e := range all {
		line := e.Text
		if e.Tag != "" {
			line = "[" + e.Tag + "] " + line
		}
		b.WriteString("- " + line + "\n")
		if maxChars > 0 && b.Len() >= maxChars {
			b.WriteString("- …（更多记忆见 /pmem list）\n")
			break
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
