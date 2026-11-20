package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJournalBeginProgressFinish(t *testing.T) {
	dir := t.TempDir()
	j := NewJournal(dir)
	if err := j.Begin(JournalEntry{ConvID: "conv-1", UserText: "把下载目录的发票整理一下"}); err != nil {
		t.Fatal(err)
	}
	pending, err := j.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ConvID != "conv-1" {
		t.Fatalf("应有一条待恢复记录，实际 %+v", pending)
	}
	// 进度更新要保留原任务描述，否则恢复提示就丢了上下文
	if err := j.Progress("conv-1", 3, "edit_file"); err != nil {
		t.Fatal(err)
	}
	pending, _ = j.Pending()
	if pending[0].Turn != 3 || pending[0].LastTool != "edit_file" {
		t.Fatalf("进度未更新：%+v", pending[0])
	}
	if pending[0].UserText == "" {
		t.Fatal("进度更新不应丢掉原任务描述")
	}
	// 正常收尾后不再提示
	if err := j.Finish("conv-1"); err != nil {
		t.Fatal(err)
	}
	if p, _ := j.Pending(); len(p) != 0 {
		t.Fatalf("收尾后不应再有记录：%+v", p)
	}
}

// 掉电时可能正好写坏日志：半截 JSON 必须被识别并丢弃，不能挡住启动。
func TestJournalCorruptFileDropped(t *testing.T) {
	dir := t.TempDir()
	j := NewJournal(dir)
	os.WriteFile(filepath.Join(dir, "bad"+journalExt), []byte(`{"conv_id":"x","turn":`), 0o644)
	pending, err := j.Pending()
	if err != nil {
		t.Fatalf("损坏文件不应让 Pending 报错：%v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("损坏记录应被丢弃：%+v", pending)
	}
	if _, err := os.Stat(filepath.Join(dir, "bad"+journalExt)); !os.IsNotExist(err) {
		t.Fatal("损坏文件应被清理")
	}
}

// 日志本身也是原子写：写完不能留下 .tmp 残留。
func TestJournalAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	j := NewJournal(dir)
	j.Begin(JournalEntry{ConvID: "conv-2", UserText: "x"})
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("不应残留临时文件：%s", e.Name())
		}
	}
}

// 会话 ID 里带路径分隔符不能写出目录之外。
func TestJournalSanitizeID(t *testing.T) {
	dir := t.TempDir()
	j := NewJournal(dir)
	j.Begin(JournalEntry{ConvID: "../../evil", UserText: "x"})
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("应只写一个文件，实际 %d", len(ents))
	}
	if strings.Contains(ents[0].Name(), "/") {
		t.Fatalf("文件名不应含分隔符：%s", ents[0].Name())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil"+journalExt)); err == nil {
		t.Fatal("不应写到上级目录")
	}
}

func TestJournalExpiredDropped(t *testing.T) {
	dir := t.TempDir()
	j := NewJournal(dir)
	old := time.Now().Add(-10 * 24 * time.Hour)
	j.Begin(JournalEntry{ConvID: "old", StartedAt: old, UserText: "很久以前"})
	if p, _ := j.Pending(); len(p) != 0 {
		t.Fatalf("过期记录应被清理：%+v", p)
	}
}

// 恢复提示必须包含时间、任务、进度三要素，否则用户看不懂发生了什么。
func TestRecoveryText(t *testing.T) {
	e := JournalEntry{
		ConvID:    "c",
		UserText:  "给设置页加一个深色模式开关",
		Turn:      2,
		LastTool:  "write_file",
		StartedAt: time.Date(2026, 11, 13, 21, 30, 0, 0, time.Local),
	}
	notice := RecoveryNotice(e)
	for _, want := range []string{"中断", "11-13 21:30", "深色模式", "第 2 轮", "write_file", "/resume"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("恢复提示缺少 %q：\n%s", want, notice)
		}
	}
	prompt := RecoveryPrompt(e)
	for _, want := range []string{"核对当前代码", "不要重复", "验证"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("恢复指令缺少 %q：\n%s", want, prompt)
		}
	}
}

// 超长任务描述要截断，中文不能截成乱码。
func TestClipRunes(t *testing.T) {
	s := strings.Repeat("发票", 200)
	got := clipRunes(s, 10)
	if len([]rune(got)) != 11 {
		t.Fatalf("截断长度异常：%d", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatal("截断应有省略号")
	}
	if clipRunes("短", 10) != "短" {
		t.Fatal("短文本不应变化")
	}
}
