package core

import (
	"path/filepath"
	"testing"
)

func TestTaskQueuePersistAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")

	kv1, err := OpenKV(path)
	if err != nil {
		t.Fatal(err)
	}
	q1 := NewTaskQueue(64, kv1)
	if r := q1.Push(TaskItem{Prompt: "A", Dedup: "sched-1@1000"}); !r.OK {
		t.Fatalf("Push A: %+v", r)
	}
	if r := q1.Push(TaskItem{Prompt: "B", Dedup: "sched-2@2000"}); !r.OK {
		t.Fatalf("Push B: %+v", r)
	}
	if r := q1.Push(TaskItem{Prompt: "A2", Dedup: "sched-1@1000"}); r.OK {
		t.Fatal("同一 dedup 不应入队")
	}
	if err := kv1.Close(); err != nil {
		t.Fatal(err)
	}

	kv2, err := OpenKV(path)
	if err != nil {
		t.Fatal(err)
	}
	defer kv2.Close()
	q2 := NewTaskQueue(64, kv2)
	if q2.Len() != 2 {
		t.Fatalf("重启后队列长度: got %d want 2", q2.Len())
	}
	items := q2.List()
	if items[0].Prompt != "A" || items[1].Prompt != "B" {
		t.Fatalf("恢复顺序/内容不符: %+v", items)
	}
	// 重启后 dedup 从 items 重建，同键仍应被拒
	if r := q2.Push(TaskItem{Prompt: "A3", Dedup: "sched-1@1000"}); r.OK {
		t.Fatal("重启后同一 dedup 仍不应入队")
	}
}

func TestTaskQueuePopThenReopenReleasesDedup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	kv1, err := OpenKV(path)
	if err != nil {
		t.Fatal(err)
	}
	q1 := NewTaskQueue(64, kv1)
	q1.Push(TaskItem{Prompt: "A", Dedup: "k1"})
	it, ok := q1.Pop()
	if !ok || it.Prompt != "A" {
		t.Fatalf("Pop: ok=%v it=%+v", ok, it)
	}
	if err := kv1.Close(); err != nil {
		t.Fatal(err)
	}

	kv2, err := OpenKV(path)
	if err != nil {
		t.Fatal(err)
	}
	defer kv2.Close()
	q2 := NewTaskQueue(64, kv2)
	if q2.Len() != 0 {
		t.Fatalf("Pop 后队列应为空: got %d", q2.Len())
	}
	// 已在 Pop 后崩溃：任务没处理完，dedup 不应残留，同键可重新入队
	if r := q2.Push(TaskItem{Prompt: "A-retry", Dedup: "k1"}); !r.OK {
		t.Fatalf("崩溃后同键应可重新入队: %+v", r)
	}
}

func TestTaskQueueWithoutKVStaysInMemory(t *testing.T) {
	q := NewTaskQueue(4)
	q.Push(TaskItem{Prompt: "x"})
	if q.Len() != 1 {
		t.Fatalf("纯内存模式队列长度: %d", q.Len())
	}
}
