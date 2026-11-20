package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestKVPutGetRoundTrip(t *testing.T) {
	kv, err := OpenKV(filepath.Join(t.TempDir(), "kv.db"))
	if err != nil {
		t.Fatalf("OpenKV: %v", err)
	}
	defer kv.Close()

	type rec struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	if err := kv.Put("tasks", "a", rec{Name: "alpha", N: 1}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	var got rec
	ok, err := kv.Get("tasks", "a", &got)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Name != "alpha" || got.N != 1 {
		t.Fatalf("往返内容不符: %+v", got)
	}

	ok, err = kv.Get("tasks", "missing", &got)
	if err != nil || ok {
		t.Fatalf("不存在的 key 应返回 ok=false: ok=%v err=%v", ok, err)
	}
}

func TestKVPersistAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kv.db")

	kv, err := OpenKV(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Put("cfg", "k", "value-持久"); err != nil {
		t.Fatal(err)
	}
	if err := kv.Close(); err != nil {
		t.Fatal(err)
	}

	kv2, err := OpenKV(path)
	if err != nil {
		t.Fatal(err)
	}
	defer kv2.Close()
	var got string
	ok, err := kv2.Get("cfg", "k", &got)
	if err != nil || !ok || got != "value-持久" {
		t.Fatalf("重开后数据丢失: ok=%v got=%q err=%v", ok, got, err)
	}
}

func TestKVUpdateRollbackOnError(t *testing.T) {
	kv, err := OpenKV(filepath.Join(t.TempDir(), "kv.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer kv.Close()

	boom := errors.New("boom")
	err = kv.Update(func(tx *KVTx) error {
		if e := tx.Put("b", "k1", []byte("v1")); e != nil {
			return e
		}
		if e := tx.Put("b", "k2", []byte("v2")); e != nil {
			return e
		}
		return boom // 两处写入都应回滚
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Update 应透传错误: %v", err)
	}

	err = kv.View(func(tx *KVTxView) error {
		for _, k := range []string{"k1", "k2"} {
			_, ok, e := tx.Get("b", k)
			if e != nil {
				return e
			}
			if ok {
				return fmt.Errorf("回滚失败: %s 仍存在", k)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestKVConcurrentUpdates(t *testing.T) {
	kv, err := OpenKV(filepath.Join(t.TempDir(), "kv.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer kv.Close()

	const n = 50
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := kv.Update(func(tx *KVTx) error {
				key := fmt.Sprintf("k%02d", i)
				return tx.Put("c", key, []byte(fmt.Sprintf("v%02d", i)))
			})
			if e != nil {
				errCh <- e
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for e := range errCh {
		t.Fatalf("并发 Update 出错: %v", e)
	}

	count := 0
	err = kv.View(func(tx *KVTxView) error {
		b := tx.tx.Bucket([]byte("c"))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			count++
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != n {
		t.Fatalf("并发写入条数不符: got %d want %d", count, n)
	}
}

func TestKVFilePerm0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kv.db")
	kv, err := OpenKV(path)
	if err != nil {
		t.Fatal(err)
	}
	kv.Close()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// Windows 下 Go 不把 Unix 权限位映射到 ACL：os.Stat 对普通文件恒报 0666，
		// bbolt 的 0600 参数由 OS 按自身语义处理（DataDir 仍受 Windows ACL 保护）。
		// 这里只断言它是普通文件，权限位的严格校验交给非 Windows 平台。
		if !st.Mode().IsRegular() {
			t.Fatalf("数据库文件不是普通文件: %v", st.Mode())
		}
		return
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("数据库文件权限过宽: %o", st.Mode().Perm())
	}
}

func TestKVDeleteAndForEach(t *testing.T) {
	kv, err := OpenKV(filepath.Join(t.TempDir(), "kv.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer kv.Close()

	if err := kv.Update(func(tx *KVTx) error {
		for _, k := range []string{"a", "b", "c"} {
			if e := tx.Put("d", k, []byte(k)); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := kv.Update(func(tx *KVTx) error { return tx.Delete("d", "b") }); err != nil {
		t.Fatal(err)
	}

	var keys []string
	err = kv.View(func(tx *KVTxView) error {
		return tx.tx.Bucket([]byte("d")).ForEach(func(k, v []byte) error {
			keys = append(keys, string(k))
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "c" {
		t.Fatalf("ForEach 结果不符: %v", keys)
	}
}
