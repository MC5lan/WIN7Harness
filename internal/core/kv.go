package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// 事务型本地键值存储（bbolt：纯 Go B+tree，无 cgo）。
//
// 为什么引入：项目原有持久化全靠 "JSON 文件 + 自写原子写"（WriteFileAtomic），
// 单文件写入是原子的，但"读-改-写"跨进程没有事务语义；快照链也只是
// "文件 + 元数据"的约定，崩溃一致性靠一堆 if。bbolt 提供真正的 ACID 事务：
// 一个事务里多 key 的改动要么全落盘、要么全不落，断电不撕裂。
//
// Win7 / 32 位适配要点：
//   - bbolt 纯 Go 实现，CGO_ENABLED=0 下可编译，386/amd64 均可；
//   - 单文件 + 文件锁，同一进程重复 Open 会阻塞，因此 DataKV() 做成单例；
//   - 文件权限 0600：内容含工作区路径/提示词，不能被同机其它用户读；
//   - bbolt 每次提交自行 fsync，断电后已提交事务不丢（不依赖上层 Sync）。
//
// 注意：本项目已有的 JSON 持久化（config / sessions / snapshots / 三份清单）
// 保持不变；KV 是新数据的落点，不读写旧格式，避免迁移风险。

const kvFileName = "harness.kv.db"

var (
	dataKVOnce sync.Once
	dataKVInst *KV
	dataKVErr  error
)

// KVPath 返回进程级 KV 数据库路径。
func KVPath() string { return filepath.Join(LocalDataDir(), kvFileName) }

// DataKV 返回进程级单例 KV（路径 DataDir/harness.kv.db）。
// 首次调用打开数据库并建好文件；并发调用安全。
func DataKV() (*KV, error) {
	dataKVOnce.Do(func() {
		dataKVInst, dataKVErr = OpenKV(KVPath())
	})
	return dataKVInst, dataKVErr
}

// KV 是对 bbolt 的薄封装。调用方拿到后应长期持有，不再重复 Open 同一文件。
type KV struct {
	db *bolt.DB
}

// OpenKV 打开（或创建）一个 KV 数据库。
// 父目录不存在时创建；文件权限 0600。
func OpenKV(path string) (*KV, error) {
	if path == "" {
		return nil, errors.New("kv: 未指定路径")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("kv: 创建目录失败: %w", err)
		}
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("kv: 打开数据库失败: %w", err)
	}
	return &KV{db: db}, nil
}

// Close 关闭数据库。进程退出前调用；不调用则由 OS 回收文件锁。
func (kv *KV) Close() error {
	if kv == nil || kv.db == nil {
		return nil
	}
	return kv.db.Close()
}

// CreateBucket 确保 bucket 存在（幂等）。
func (kv *KV) CreateBucket(name string) error {
	if name == "" {
		return errors.New("kv: bucket 名为空")
	}
	return kv.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(name))
		return err
	})
}

// txGet / txPut / txDelete 是事务内对 KV 的最小操作，供 View/Update 回调使用。
// 调用方必须在对应事务回调内使用，跨事务使用会失效。

// KVTx 是写事务句柄。回调内可多次 Put/Delete，全部在提交时一次性落盘。
type KVTx struct {
	tx *bolt.Tx
}

// Get 读取 key，第三个返回值为 false 表示 key 不存在。
func (t *KVTx) Get(bucket, key string) ([]byte, bool, error) {
	b := t.tx.Bucket([]byte(bucket))
	if b == nil {
		return nil, false, nil
	}
	v := b.Get([]byte(key))
	if v == nil {
		return nil, false, nil
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, true, nil
}

// Put 写入 key（事务提交时落盘）。
func (t *KVTx) Put(bucket, key string, value []byte) error {
	b, err := t.tx.CreateBucketIfNotExists([]byte(bucket))
	if err != nil {
		return err
	}
	return b.Put([]byte(key), value)
}

// Delete 删除 key；key 不存在不报错。
func (t *KVTx) Delete(bucket, key string) error {
	b := t.tx.Bucket([]byte(bucket))
	if b == nil {
		return nil
	}
	return b.Delete([]byte(key))
}

// ForEach 遍历 bucket 的全部键值对（按 key 字典序）。
// 回调返回 err 会终止事务；返回 errStopKV 可提前结束且不报错。
func (t *KVTx) ForEach(bucket string, fn func(k, v []byte) error) error {
	b := t.tx.Bucket([]byte(bucket))
	if b == nil {
		return nil
	}
	return b.ForEach(fn)
}

// KVTxView 是只读事务句柄（无 Put/Delete）。
type KVTxView struct{ tx *bolt.Tx }

// Get 读取 key。
func (t *KVTxView) Get(bucket, key string) ([]byte, bool, error) {
	b := t.tx.Bucket([]byte(bucket))
	if b == nil {
		return nil, false, nil
	}
	v := b.Get([]byte(key))
	if v == nil {
		return nil, false, nil
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, true, nil
}

// View 在只读事务中执行 fn。多个 View 可并发；View 与 Update 互斥。
func (kv *KV) View(fn func(tx *KVTxView) error) error {
	return kv.db.View(func(tx *bolt.Tx) error {
		return fn(&KVTxView{tx: tx})
	})
}

// Update 在写事务中执行 fn。回调返回错误则整体回滚；返回 nil 则一次性提交。
// 这是本封装的核心：跨多个 key 的改动具备原子性。
func (kv *KV) Update(fn func(tx *KVTx) error) error {
	return kv.db.Update(func(tx *bolt.Tx) error {
		return fn(&KVTx{tx: tx})
	})
}

// Get 读取单个 key 并反序列化为 JSON。找不到时返回 ok=false。
func (kv *KV) Get(bucket, key string, out interface{}) (bool, error) {
	var found bool
	err := kv.View(func(tx *KVTxView) error {
		raw, ok, err := tx.Get(bucket, key)
		if err != nil || !ok {
			found = ok
			return err
		}
		found = true
		return json.Unmarshal(raw, out)
	})
	return found, err
}

// Put 将 value 以 JSON 写入单个 key（单 key 自动原子）。
func (kv *KV) Put(bucket, key string, value interface{}) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return kv.Update(func(tx *KVTx) error {
		return tx.Put(bucket, key, raw)
	})
}
