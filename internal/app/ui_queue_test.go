//go:build windows

package app

import (
	"sync"
	"sync/atomic"
	"testing"
)

// 后台 goroutine 会并发 queueUI；队列必须有锁，Timer 消费时不能丢动作。
func TestUIQueueConcurrent(t *testing.T) {
	a := &App{}
	const n = 200
	var count int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.queueUI(func() { atomic.AddInt32(&count, 1) })
		}()
	}
	wg.Wait()
	a.runUIQueue()
	if got := atomic.LoadInt32(&count); got != n {
		t.Fatalf("排队动作丢失：got %d want %d", got, n)
	}
}

// 动作里再次 queueUI 应落到下一轮，不能在同一帧无限递归。
func TestUIQueueReentrant(t *testing.T) {
	a := &App{}
	var second int32
	a.queueUI(func() {
		a.queueUI(func() { atomic.StoreInt32(&second, 1) })
	})
	a.runUIQueue()
	if atomic.LoadInt32(&second) != 0 {
		t.Fatal("同一轮不应执行新排入的动作")
	}
	a.runUIQueue()
	if atomic.LoadInt32(&second) != 1 {
		t.Fatal("下一轮应执行新排入的动作")
	}
}
