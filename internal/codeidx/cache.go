package codeidx

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"win7harness/internal/core"
)

// —— 索引缓存：格式版本、压缩与冷启动统计 ——
//
// v1.26 之前缓存是"能读就吃"：只要是同一个 root 就直接塞进内存。
// 问题出在升级后：解析规则变了（新增了泛型符号、改了预算），旧缓存仍然有效，
// 于是用户升级后的第一次搜索拿到的是上一版规则产出的索引，名字对、行号错，
// 而且后台重建完成前一直错。这里给缓存加两道闸：
//  1. Schema：缓存格式版本，改字段就 +1，旧文件直接丢弃重建；
//  2. Rules：解析规则指纹（扩展名集合 + 预算档位），规则变了同样丢弃。
//
// 另外把 JSON 换成 gzip。几万个符号的原始 JSON 通常 8~20MB，
// 老机器机械盘顺序读要 200ms 以上；压缩后体积降到 1/6，解压耗 CPU 但
// 老 CPU 解 3MB 只要几十毫秒，净收益明显。

// CacheSchema 缓存结构版本。Index 的字段或语义变化时必须 +1。
const CacheSchema = 3

// RulesVersion 解析规则指纹。改动 parseGoFile / parseGenericFile 的输出格式、
// 扩展名白名单或预算档位时，把它改掉即可让全量旧缓存失效。
const RulesVersion = "v1.29.0"

var (
	lastLoadMs   int64
	lastLoadSyms int64
	lastLoadHit  int32
	lastLoadSize int64
)

// CacheStats 一次缓存载入的可观测结果，供 /index 与自检回显。
type CacheStats struct {
	Hit        bool   `json:"hit"`
	Ms         int64  `json:"ms"`
	Symbols    int64  `json:"symbols"`
	Bytes      int64  `json:"bytes"`
	Reason     string `json:"reason,omitempty"` // 未命中原因
	Schema     int    `json:"schema"`
	Rules      string `json:"rules"`
	Compressed bool   `json:"compressed"`
}

// LastCacheStats 返回最近一次 LoadCached 的结果（零值表示从未尝试）。
func LastCacheStats() CacheStats {
	return CacheStats{
		Hit:     atomic.LoadInt32(&lastLoadHit) == 1,
		Ms:      atomic.LoadInt64(&lastLoadMs),
		Symbols: atomic.LoadInt64(&lastLoadSyms),
		Bytes:   atomic.LoadInt64(&lastLoadSize),
		Schema:  CacheSchema,
		Rules:   RulesVersion,
	}
}

func noteLoad(hit bool, ms int64, syms, size int64) {
	if hit {
		atomic.StoreInt32(&lastLoadHit, 1)
	} else {
		atomic.StoreInt32(&lastLoadHit, 0)
	}
	atomic.StoreInt64(&lastLoadMs, ms)
	atomic.StoreInt64(&lastLoadSyms, syms)
	atomic.StoreInt64(&lastLoadSize, size)
}

// CacheValid 判断缓存是否可用于当前版本。
func (ix *Index) CacheValid() (bool, string) {
	if ix == nil {
		return false, "empty"
	}
	if ix.Schema != CacheSchema {
		return false, "schema"
	}
	if ix.Rules != RulesVersion {
		return false, "rules"
	}
	if ix.Root == "" || len(ix.Syms) == 0 && len(ix.Files) == 0 {
		return false, "empty"
	}
	return true, ""
}

// gzipBytes 压缩。
func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// readMaybeGzip 读文件，自动识别 gzip（兼容旧版明文缓存）。
func readMaybeGzip(p string) ([]byte, bool, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, false, err
	}
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, true, err
		}
		defer zr.Close()
		out, err := io.ReadAll(io.LimitReader(zr, 256<<20))
		if err != nil {
			return nil, true, err
		}
		return out, true, nil
	}
	return raw, false, nil
}

// writeCacheFile 把索引编码、压缩后原子落盘。压缩失败就写明文——
// 缓存坏了只是慢一点，不该让索引功能整体失效。
func writeCacheFile(p string, ix *Index) (int64, error) {
	data, err := json.Marshal(ix)
	if err != nil {
		return 0, err
	}
	out, gzErr := gzipBytes(data)
	if gzErr != nil {
		out = data
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", p, os.Getpid(), time.Now().UnixNano())
	if err := core.WriteFileAtomic(tmp, out, 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	return int64(len(out)), nil
}

// loadCacheFile 读索引、校验版本，返回索引与统计。
func loadCacheFile(p, root string) (*Index, CacheStats) {
	start := time.Now()
	st := CacheStats{Schema: CacheSchema, Rules: RulesVersion}
	fi, statErr := os.Stat(p)
	if statErr != nil {
		st.Reason = "miss"
		st.Ms = time.Since(start).Milliseconds()
		return nil, st
	}
	st.Bytes = fi.Size()
	data, compressed, err := readMaybeGzip(p)
	if err != nil || len(data) == 0 {
		st.Reason = "corrupt"
		st.Ms = time.Since(start).Milliseconds()
		return nil, st
	}
	st.Compressed = compressed
	var ix Index
	if err := json.Unmarshal(data, &ix); err != nil {
		st.Reason = "corrupt"
		st.Ms = time.Since(start).Milliseconds()
		return nil, st
	}
	if !samePath(ix.Root, root) {
		st.Reason = "root"
		st.Ms = time.Since(start).Milliseconds()
		return nil, st
	}
	if ok, why := ix.CacheValid(); !ok {
		st.Reason = why
		st.Ms = time.Since(start).Milliseconds()
		return nil, st
	}
	if ix.Files == nil {
		ix.Files = map[string]int64{}
	}
	if ix.Sizes == nil {
		ix.Sizes = map[string]int64{}
	}
	st.Hit = true
	st.Ms = time.Since(start).Milliseconds()
	st.Symbols = int64(len(ix.Syms))
	return &ix, st
}
