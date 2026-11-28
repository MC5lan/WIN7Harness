package codeidx

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func resetCallGraphCache() {
	ResetGraph()
	graphFileMu.Lock()
	graphFiles = map[string]*fileGraph{}
	graphFileMu.Unlock()
	atomic.StoreInt64(&graphParsed, 0)
}

func writeGo(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCallGraphOnlyReparsesChangedFile(t *testing.T) {
	resetCallGraphCache()
	defer resetCallGraphCache()

	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	writeGo(t, a, "package p\nfunc A() { B() }\n")
	writeGo(t, b, "package p\nfunc B() {}\n")

	g := BuildGraph(dir)
	if g.Files != 2 || g.Funcs != 2 {
		t.Fatalf("初始图异常：files=%d funcs=%d", g.Files, g.Funcs)
	}
	if got := atomic.LoadInt64(&graphParsed); got != 2 {
		t.Fatalf("初始应解析 2 个文件，实际 %d", got)
	}
	if len(g.Callers("B", 10)) != 1 {
		t.Fatalf("初始 B 调用点丢失：%+v", g.Callers("B", 10))
	}

	before := atomic.LoadInt64(&graphParsed)
	writeGo(t, a, "package p\nfunc A() { C() }\n")
	// 明确推进 mtime，避免文件系统时间精度导致缓存误命中。
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(a, future, future); err != nil {
		t.Fatal(err)
	}
	MarkDirtyAbs(a)
	takeDirty() // 清掉脏文件登记，避免影响其他用例

	g2 := BuildGraph(dir)
	parsed := atomic.LoadInt64(&graphParsed) - before
	if parsed != 1 {
		t.Fatalf("只改 a.go 时应只重解析 1 个文件，实际 %d", parsed)
	}
	if len(g2.Callers("B", 10)) != 0 {
		t.Fatalf("旧调用关系未失效：%+v", g2.Callers("B", 10))
	}
	if len(g2.Callers("C", 10)) != 1 {
		t.Fatalf("新调用关系未建立：%+v", g2.Callers("C", 10))
	}
}

func TestCallGraphDetectsMtimeChangeWithoutDirty(t *testing.T) {
	resetCallGraphCache()
	defer resetCallGraphCache()

	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	writeGo(t, a, "package p\nfunc A() { B() }\n")
	BuildGraph(dir)
	before := atomic.LoadInt64(&graphParsed)

	writeGo(t, a, "package p\nfunc A() { C() }\n")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(a, future, future); err != nil {
		t.Fatal(err)
	}
	g := BuildGraph(dir)
	if got := atomic.LoadInt64(&graphParsed) - before; got != 1 {
		t.Fatalf("mtime 变化应触发重解析，实际 %d", got)
	}
	if len(g.Callers("C", 10)) != 1 {
		t.Fatalf("新调用关系未建立：%+v", g.Callers("C", 10))
	}
}

func TestGraphCachesByRoot(t *testing.T) {
	resetCallGraphCache()
	defer resetCallGraphCache()

	dir := t.TempDir()
	writeGo(t, filepath.Join(dir, "a.go"), "package p\nfunc A() {}\n")
	g1 := Graph(dir)
	g2 := Graph(dir)
	if g1 != g2 {
		t.Fatal("同一 root 应复用 Graph 缓存")
	}
}
