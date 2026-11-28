package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskQueueDedupAndOrder(t *testing.T) {
	q := NewTaskQueue(4)
	a := q.Push(TaskItem{Prompt: "A", Dedup: "sched-1@1000"})
	if !a.OK {
		t.Fatalf("首次入队应成功，实际：%s", a.Reason)
	}
	if r := q.Push(TaskItem{Prompt: "A2", Dedup: "sched-1@1000"}); r.OK {
		t.Fatal("同一触发点重复入队应被拒绝")
	}
	if r := q.Push(TaskItem{Prompt: "B", Dedup: "sched-1@2000"}); !r.OK {
		t.Fatal("不同触发点应能入队")
	}
	if q.Len() != 2 {
		t.Fatalf("队列长度应为 2，实际 %d", q.Len())
	}
	first, ok := q.Pop()
	if !ok || first.Prompt != "A" {
		t.Fatalf("应先进先出取到 A，实际 %+v", first)
	}
	// 出队后去重键仍然保留（防止同一到期点被重复塞回来），Done 才释放。
	if r := q.Push(TaskItem{Prompt: "A3", Dedup: "sched-1@1000"}); r.OK {
		t.Fatal("未 Done 之前同键不应再次入队")
	}
	q.Done("sched-1@1000")
	if r := q.Push(TaskItem{Prompt: "A4", Dedup: "sched-1@1000"}); !r.OK {
		t.Fatal("Done 之后同键应允许再次入队")
	}
}

func TestTaskQueueCapAndClear(t *testing.T) {
	q := NewTaskQueue(2)
	q.Push(TaskItem{Prompt: "1"})
	q.Push(TaskItem{Prompt: "2"})
	if r := q.Push(TaskItem{Prompt: "3"}); r.OK {
		t.Fatal("超出上限应拒绝")
	} else if r.Reason == "" {
		t.Fatal("拒绝时必须给出原因，不能静默丢弃")
	}
	if n := q.Clear(); n != 2 {
		t.Fatalf("Clear 应返回 2，实际 %d", n)
	}
	if q.Len() != 0 {
		t.Fatal("Clear 后队列应为空")
	}
	if _, ok := q.Pop(); ok {
		t.Fatal("空队列 Pop 应返回 false")
	}
}

func TestTaskQueueEvictSeen(t *testing.T) {
	q := NewTaskQueue(1000)
	for i := 0; i < 300; i++ {
		q.Push(TaskItem{Prompt: "x", Dedup: "k" + itoa(int64(i))})
	}
	if len(q.order) > 256 {
		t.Fatalf("去重键应被淘汰到 256 以内，实际 %d", len(q.order))
	}
	// 被淘汰的旧键可以重新入队（不会无限增长，也不会误判）。
	if r := q.Push(TaskItem{Prompt: "old", Dedup: "k0"}); !r.OK {
		t.Fatal("已淘汰的键应可重新入队")
	}
}

func TestApplyScriptRollback(t *testing.T) {
	s := ApplyScript(`C:\tools\harness.exe`, `C:\tools\updates\new.exe`, true)
	for _, want := range []string{
		`set "TARGET=C:\tools\harness.exe"`,
		`set "NEW=C:\tools\updates\new.exe"`,
		`set "OLD=%TARGET%.old"`,
		`set "HEALTH=`,
		`set /a TRIES+=1`,
		`:health`,
		`:healthfail`,
		`taskkill /f /im "%NAME%"`,
		`:rollback`,
		`:giveup`,
		`start "" "%TARGET%"`,
	} {
		if !containsStr(s, want) {
			t.Fatalf("脚本缺少关键片段 %q\n%s", want, s)
		}
	}
	// 不带重启时不应出现 start
	if s2 := ApplyScript("a.exe", "b.exe", false); containsStr(s2, `start "" "%TARGET%"`) {
		t.Fatal("relaunch=false 时不应启动新进程")
	}
}

func TestWriteApplyScriptAndCleanup(t *testing.T) {
	dir := t.TempDir()
	p, err := WriteApplyScript(dir, "@echo off\r\n")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != dir {
		t.Fatalf("脚本应写在指定目录，实际 %s", p)
	}
	if st, err := os.Stat(p); err != nil || st.Size() == 0 {
		t.Fatal("脚本文件应存在且非空")
	}
}

func TestMarkUpdateHealthy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	if err := MarkUpdateHealthy(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(UpdateHealthPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), Version) {
		t.Fatalf("健康标记缺少版本号：%q", b)
	}
}

func TestExeArchRejectsBadFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.exe")
	if err := os.WriteFile(bad, []byte("not a pe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ExeArch(bad); err == nil {
		t.Fatal("非 PE 文件应报错，不能当成 unknown 放行")
	}
}

func TestDevicesAddVerifyRevoke(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.json")
	ds := LoadDevices(path)
	dv, err := ds.Add("小米手机", "tok-abc", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if dv.TokenHash == "tok-abc" {
		t.Fatal("落盘不能存明文 token")
	}
	// 重新加载：token 仍在（这是 v1.28 内存态做不到的）
	ds2 := LoadDevices(path)
	if _, ok := ds2.Verify("tok-abc", time.Now()); !ok {
		t.Fatal("重启后 token 应仍然有效")
	}
	if _, ok := ds2.Verify("wrong", time.Now()); ok {
		t.Fatal("错误 token 不应通过")
	}
	// 过期
	if _, ok := ds2.Verify("tok-abc", time.Now().Add(48*time.Hour)); ok {
		t.Fatal("过期 token 不应通过")
	}
	if _, ok := ds2.Revoke("小米手机"); !ok {
		t.Fatal("按名字应能吊销")
	}
	if _, ok := LoadDevices(path).Verify("tok-abc", time.Now()); ok {
		t.Fatal("吊销后 token 应失效")
	}
}

func TestDevicesCorruptQuarantine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.json")
	if err := os.WriteFile(path, []byte("{坏掉的 json"), 0o600); err != nil {
		t.Fatal(err)
	}
	ds := LoadDevices(path)
	if ds.LoadErr == "" {
		t.Fatal("损坏时必须给出可见原因，不能静默当空表")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !containsStr(entries[0].Name(), ".bad-") {
		t.Fatalf("应留下 .bad- 备份，实际 %v", entries)
	}
}

func containsStr(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
