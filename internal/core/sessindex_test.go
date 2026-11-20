package core

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 会话索引流式解析：摘要与 SessionLines 窗口语义必须与旧实现一致。

func msg(role, text string, at string, tools ...string) map[string]interface{} {
	m := map[string]interface{}{
		"role": role,
		"text": text,
		"at":   at,
	}
	if len(tools) > 0 {
		tl := make([]map[string]interface{}, 0, len(tools))
		for _, t := range tools {
			tl = append(tl, map[string]interface{}{"name": t})
		}
		m["tools"] = tl
	}
	return m
}

func writeSessionFixture(t *testing.T, dir, id, title, convAt string, msgs []map[string]interface{}) string {
	t.Helper()
	conv := map[string]interface{}{
		"v": 1,
		"conv": map[string]interface{}{
			"id":    id,
			"title": title,
			"at":    convAt,
			"msgs":  msgs,
		},
	}
	data, err := json.Marshal(conv)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSessionIndexListsAndPreviews(t *testing.T) {
	dir := t.TempDir()
	writeSessionFixture(t, dir, "aaaa000000000001", "旧会话", "2026-11-14T10:03:30+08:00", []map[string]interface{}{
		msg("user", "第一条", "2026-11-14T10:00:00+08:00"),
	})
	writeSessionFixture(t, dir, "bbbb000000000002", "新会话\n带换行的标题", "2026-11-17T09:00:00+08:00", []map[string]interface{}{
		msg("user", "问题一", "2026-11-16T09:00:00+08:00"),
		msg("assistant", "回答一", "2026-11-16T09:01:00+08:00"),
		msg("assistant", "最后一行\n第二行", "2026-11-16T09:02:00+08:00"),
	})
	list, err := SessionIndex(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("应有 2 个会话，实际 %d", len(list))
	}
	// 按 At 倒序 → 新会话在前
	if list[0].ID != "bbbb000000000002" {
		t.Fatalf("排序错误：%+v", list)
	}
	if list[0].Msgs != 3 {
		t.Fatalf("消息数应为 3，实际 %d", list[0].Msgs)
	}
	if list[0].Preview == "" || strings.Contains(list[0].Preview, "\n") {
		t.Fatalf("preview 应为单行，实际 %q", list[0].Preview)
	}
	if !strings.Contains(list[0].Preview, "第二行") {
		t.Fatalf("preview 应取最后一条非空消息，实际 %q", list[0].Preview)
	}
	if list[0].Size <= 0 {
		t.Fatalf("size 应为文件字节数，实际 %d", list[0].Size)
	}
	// 标题换行应原样返回（UI 截断），这里只验证没丢数据
	if list[0].Title == "" {
		t.Fatal("标题不应为空")
	}
}

func TestSessionLinesWindowAndTools(t *testing.T) {
	dir := t.TempDir()
	id := "cccc000000000003"
	writeSessionFixture(t, dir, id, "窗口测试", "2026-11-16T10:03:30+08:00", []map[string]interface{}{
		msg("user", "m0", "2026-11-16T10:00:00+08:00"),
		msg("assistant", "", "2026-11-16T10:00:01+08:00", "read_file", "grep"), // 只有工具 → 工具行
		msg("", "", "2026-11-16T10:00:02+08:00"),                               // 完全空 → 跳过
		msg("user", "m3", "2026-11-16T10:00:03+08:00"),
		msg("assistant", "m4", "2026-11-16T10:00:04+08:00"),
	})
	lines, total, err := SessionLines(dir, id, 0, 60)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Fatalf("total 应为 5，实际 %d", total)
	}
	// 空消息被跳过：m0, 工具行, m3, m4
	if len(lines) != 4 {
		t.Fatalf("应有 4 行（空消息跳过），实际 %d", len(lines))
	}
	if lines[1].Tool != "read_file,grep" {
		t.Fatalf("工具名提取错误：%q", lines[1].Tool)
	}
	// 窗口：from=2, max=2 → 保留 m3, m4（第 3、4 条）
	lines, total, err = SessionLines(dir, id, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Fatalf("from 不影响 total，应为 5，实际 %d", total)
	}
	if len(lines) != 2 || lines[0].Text != "m3" || lines[1].Text != "m4" {
		t.Fatalf("窗口取值错误：%+v", lines)
	}
	// from 超过 total → 空行 + 真实 total
	lines, total, err = SessionLines(dir, id, 99, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(lines) != 0 {
		t.Fatalf("from>total 应返回空行+真实 total，实际 total=%d len=%d", total, len(lines))
	}
}

func TestSessionIndexCorruptSkipped(t *testing.T) {
	dir := t.TempDir()
	p := writeSessionFixture(t, dir, "dddd000000000004", "好", "2026-11-16T10:03:30+08:00", []map[string]interface{}{
		msg("user", "你好", "2026-11-16T10:00:00+08:00"),
	})
	_ = p
	if err := os.WriteFile(filepath.Join(dir, "eeee000000000005.json"), []byte("{损坏的"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := SessionIndex(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "dddd000000000004" {
		t.Fatalf("损坏会话应被跳过，实际 %+v", list)
	}
	if _, _, err := SessionLines(dir, "eeee000000000005", 0, 10); err == nil {
		t.Fatal("损坏会话的 SessionLines 应报错")
	}
}

func TestSessionIndexGzipFixture(t *testing.T) {
	dir := t.TempDir()
	id := "ffff000000000006"
	conv := map[string]interface{}{
		"v": 1,
		"conv": map[string]interface{}{
			"id":    id,
			"title": "压缩会话",
			"at":    "2026-11-17T08:00:00+08:00",
			"msgs":  []map[string]interface{}{msg("user", "压缩内容测试", "2026-11-17T08:00:01+08:00")},
		},
	}
	data, _ := json.Marshal(conv)
	f, err := os.Create(filepath.Join(dir, id+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	list, err := SessionIndex(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id || list[0].Msgs != 1 {
		t.Fatalf("gzip 会话解析失败：%+v", list)
	}
	lines, total, err := SessionLines(dir, id, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(lines) != 1 || lines[0].Text != "压缩内容测试" {
		t.Fatalf("gzip SessionLines 错误：total=%d lines=%+v", total, lines)
	}
}
