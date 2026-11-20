package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 文件快照：对标 Codex / Claude Code 的编辑回滚能力。
// 每次写入或修改文件前保存原内容，支持 /undo 逐次回退。
//
// 存储布局（v1.30 起）：
//   snapshots/<convID>/<yyyymm>/<id>.json|.bak     新写入的快照
//   snapshots/<id>.json|.bak                        旧平铺布局（继续可读/可撤销/可清理）
//
// 设计要点：
//   - 分片：按会话 + 月份两级目录，prune 只扫当前会话，几万条快照也不再全量扫；
//   - 内容去重：相邻快照内容一致（sha256 相同）时不重复写 .bak，meta 记 same_as 指向；
//     prune 扩选受害者时会把“依赖被删条”的依赖者一并删除，不留断链；
//   - 双限保留：每个会话最多 200 条 + 全局 .bak 总字节 512MB，任一超限都从最旧删；
//   - 单文件上限：超过 64MB 的文件只记路径/大小/指纹，不复制内容（否则大仓库一次写几十 GB）；
//   - 恢复安全：OverSize 条目明确报错，绝不往回写半截；same_as 链解析失败也中止，不猜。

var snapMu sync.Mutex

// SnapshotsDir 快照目录。
func SnapshotsDir() string { return filepath.Join(LocalDataDir(), "snapshots") }

const (
	snapshotRetain      = 200       // 每个会话保留条数
	snapshotMaxTotalB   = 512 << 20 // 全局 .bak 总字节上限（prune 双限之一）
	snapshotMaxFileB    = 64 << 20  // 单文件大小上限
	snapshotSameAsCheck = 400       // 去重时最多回扫的同会话条数（防长会话每次写都全目录枚举）
)

// snapshotMeta 快照元信息。
// Size 用于恢复前的完整性校验（旧快照没有该字段，零值跳过）。
type snapshotMeta struct {
	Path     string `json:"path"`
	Existed  bool   `json:"existed"`
	At       string `json:"at"`
	ConvID   string `json:"conv_id,omitempty"`
	Size     int64  `json:"size,omitempty"`
	SHA256   string `json:"sha256,omitempty"`   // .bak 内容指纹（同会话去重用）
	SameAs   string `json:"same_as,omitempty"`  // 内容与某条快照一致时，指向其 ID（不重复存 .bak）
	Oversize bool   `json:"oversize,omitempty"` // 超过大小上限：只记指纹，不留内容
}

var currentSnapConv string

// SetCurrentConv 记录当前会话，快照按会话隔离。
// 旧快照没有 conv_id 字段，仍可撤销，保证老数据兼容。
func SetCurrentConv(id string) {
	snapMu.Lock()
	currentSnapConv = id
	snapMu.Unlock()
}

func snapshotID() string {
	return fmt.Sprintf("%020d_%06d", time.Now().UnixNano(), os.Getpid()%1000)
}

// sanitizeSnapConv 把会话 ID 变成安全的目录名（防路径穿越/特殊字符）。
func sanitizeSnapConv(conv string) string {
	var b strings.Builder
	for _, r := range conv {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		s = "_none"
	}
	return s
}

// snapWriteDir 返回某会话某月份的快照目录；conv 为空（旧语义）落到 _none。
func snapWriteDir(root, conv string, t time.Time) string {
	return filepath.Join(root, sanitizeSnapConv(conv), t.Format("200601"))
}

// snapshotEntry 一条快照（已定位到磁盘上的真实路径）。
type snapshotEntry struct {
	id       string
	jsonPath string
	bakPath  string
	meta     snapshotMeta
	metaOK   bool
}

// skipNames 遍历快照树时不进入的目录名。
func skipSnapDir(name string) bool {
	return name == "staging" || name == "tmp"
}

// metaCache 缓存快照元数据：同一进程内 .json 不会原地被改写（prune 只会整体删掉它），
// 重复扫描不必重复 ReadFile + Unmarshal（本机杀软会对每次读文件付出扫描代价，
// 205 次快照 = 4 万次小文件读，看着像死循环其实全在杀软上）。
var metaCache sync.Map // path → metaCacheEntry

type metaCacheEntry struct {
	size  int64
	mtime int64
	meta  snapshotMeta
	ok    bool
}

func readSnapMeta(path string) (snapshotMeta, bool) {
	st, err := os.Stat(path)
	if err != nil {
		metaCache.Delete(path)
		return snapshotMeta{}, false
	}
	if v, ok := metaCache.Load(path); ok {
		e := v.(metaCacheEntry)
		if e.size == st.Size() && e.mtime == st.ModTime().UnixNano() {
			return e.meta, e.ok
		}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		metaCache.Delete(path)
		return snapshotMeta{}, false
	}
	var m snapshotMeta
	ok := json.Unmarshal(body, &m) == nil
	metaCache.Store(path, metaCacheEntry{size: st.Size(), mtime: st.ModTime().UnixNano(), meta: m, ok: ok})
	return m, ok
}

// scanSnapshots 枚举平铺 + 嵌套两种布局下的全部快照。
// 返回顺序按 id 升序（id 是 UnixNano，字典序=时间序）。
func scanSnapshots(root string) []snapshotEntry {
	var out []snapshotEntry
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipSnapDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".json") {
			return nil
		}
		id := strings.TrimSuffix(name, ".json")
		e := snapshotEntry{
			id:       id,
			jsonPath: path,
			bakPath:  strings.TrimSuffix(path, ".json") + ".bak",
		}
		if meta, ok := readSnapMeta(path); ok {
			e.meta = meta
			e.metaOK = true
		}
		out = append(out, e)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// findEntryByID 在整个快照树按 id 找一条（same_as 链解析用）。
func findEntryByID(root, id string) (snapshotEntry, bool) {
	var found snapshotEntry
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && skipSnapDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != id+".json" {
			return nil
		}
		e := snapshotEntry{id: id, jsonPath: path, bakPath: strings.TrimSuffix(path, ".json") + ".bak"}
		if meta, ok := readSnapMeta(path); ok {
			e.meta = meta
			e.metaOK = true
		}
		found = e
		return filepath.SkipAll
	})
	return found, found.jsonPath != ""
}

// resolveBak 解析一条快照的 .bak 物理路径；same_as 指向时递归解析链（深度上限 4）。
func resolveBak(root string, e snapshotEntry, depth int) (string, error) {
	meta := e.meta
	if meta.Oversize {
		return "", fmt.Errorf("文件过大（%d MB），快照时未保留内容，无法自动回滚；如需恢复请用外部备份", meta.Size>>20)
	}
	if meta.SameAs == "" {
		return e.bakPath, nil
	}
	if depth >= 4 {
		return "", fmt.Errorf("快照去重链过长（same_as 超过 4 层），已中止")
	}
	prev, ok := findEntryByID(root, meta.SameAs)
	if !ok {
		return "", fmt.Errorf("快照去重目标 %s 已被清理，内容不可用，已中止", meta.SameAs)
	}
	return resolveBak(root, prev, depth+1)
}

// copyFileHash 复制文件到 dst（先 tmp 后 rename 由调用方负责），并返回内容 sha256 与字节数。
func copyFileHash(dst io.Writer, src io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(dst, h), src)
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// SnapshotFile 在修改文件前保存快照。文件不存在时只记录"新建"，撤销即删除。
func SnapshotFile(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	snapMu.Lock()
	defer snapMu.Unlock()

	root := SnapshotsDir()
	now := time.Now()
	dir := snapWriteDir(root, currentSnapConv, now)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	id := snapshotID()
	meta := snapshotMeta{Path: abs, At: now.Format(time.RFC3339), ConvID: currentSnapConv}
	if st, err := os.Stat(abs); err == nil && !st.IsDir() {
		meta.Size = st.Size()
		if st.Size() > snapshotMaxFileB {
			// 大文件只留指纹：覆盖式写入的代价不该是每次都复制几十 GB，
			// 也不该在 32 位进程里 io.Copy 一个 8GB 视频——恢复时明确报错并提示外部备份。
			// Existed=true（文件本来存在，只是没副本）：Undo 会报 oversize 而不是误判为“新建文件直接删掉”。
			meta.Oversize = true
			meta.Existed = true
		} else {
			src, err := os.Open(abs)
			if err != nil {
				return err
			}
			bakTmp := filepath.Join(dir, id+".bak.tmp")
			dst, err := os.OpenFile(bakTmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				src.Close()
				return err
			}
			hash, n, cpErr := copyFileHash(dst, src)
			syncErr := dst.Sync()
			closeErr := dst.Close()
			src.Close()
			bakErr := cpErr
			if bakErr == nil {
				bakErr = syncErr
			}
			if bakErr == nil {
				bakErr = closeErr
			}
			if bakErr != nil {
				_ = os.Remove(bakTmp)
				return bakErr
			}
			meta.Size = n
			meta.SHA256 = hash
			meta.Existed = true
			// 内容去重：同会话里已有内容完全相同的 .bak 就不再落盘一份，改为指向它。
			// 只回扫同会话最近 snapshotSameAsCheck 条（绝大多数命中都是“连续两次快照没变”）。
			if sameID := findSameContent(root, currentSnapConv, hash); sameID != "" {
				_ = os.Remove(bakTmp)
				meta.SameAs = sameID
			} else if err := os.Rename(bakTmp, filepath.Join(dir, id+".bak")); err != nil {
				_ = os.Remove(bakTmp)
				return err
			}
		}
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	// 元数据走原子写：撕裂的一半 json 会让整条快照静默消失（/undo 少一步）。
	if err := WriteFileAtomic(filepath.Join(dir, id+".json"), body, 0o644); err != nil {
		return err
	}
	pruneSnapshots(root, snapshotRetain)
	return nil
}

// findSameContent 找同会话里内容指纹一致的最近一条快照（回扫最近 n 条）。
func findSameContent(root, conv, hash string) string {
	entries := scanSnapshots(root)
	n := 0
	for i := len(entries) - 1; i >= 0 && n < snapshotSameAsCheck; i-- {
		e := entries[i]
		n++
		if !e.metaOK || e.meta.ConvID != conv {
			continue
		}
		if e.meta.SHA256 == "" {
			// 没有指纹的老快照无法廉价复核，宁可不重复存，不做 same_as。
			continue
		}
		if e.meta.SHA256 == hash && e.meta.Existed && !e.meta.Oversize && e.meta.SameAs == "" {
			// 只锚定真正持有 .bak 的条目（same_as==""），链长始终 ≤1；
			// 否则内容不变时链会无限增长，回滚直接爆深度上限。
			return e.id
		}
	}
	return ""
}

// UndoResult 撤销结果。
type UndoResult struct {
	Path    string
	Existed bool // 撤销后文件是否仍存在
}

// bakPathFor 解析一条快照的 .bak 物理路径。
func bakPathFor(root string, e snapshotEntry) (string, error) {
	return resolveBak(root, e, 0)
}

// UndoLast 回滚最近一次文件改动。没有快照时返回明确错误。
func UndoLast() (UndoResult, error) {
	snapMu.Lock()
	defer snapMu.Unlock()

	root := SnapshotsDir()
	entries := scanSnapshots(root)
	if len(entries) == 0 {
		return UndoResult{}, fmt.Errorf("没有可回滚的文件改动")
	}
	// 只撤销当前会话的快照：从最新往前找，跳过别的会话留下的改动。
	var target *snapshotEntry
	for i := len(entries) - 1; i >= 0; i-- {
		m := entries[i].meta
		if !entries[i].metaOK {
			continue
		}
		if m.ConvID != "" && m.ConvID != currentSnapConv {
			continue
		}
		t := entries[i]
		target = &t
		break
	}
	if target == nil {
		return UndoResult{}, fmt.Errorf("当前会话没有可回滚的文件改动")
	}
	meta := target.meta
	res := UndoResult{Path: meta.Path, Existed: meta.Existed}
	if meta.Existed {
		bakPath, err := bakPathFor(root, *target)
		if err != nil {
			return res, err
		}
		// 恢复前校验 .bak 与元数据的大小一致；不符说明快照半截（盘满/断电），
		// 不能把半截内容写回用户文件——宁可报错并保留 .bak 供人工查看。
		if meta.Size > 0 {
			if st, err := os.Stat(bakPath); err != nil || st.Size() != meta.Size {
				return res, fmt.Errorf("快照文件损坏或不完整（.bak 大小与记录不符），已中止回滚：%s", bakPath)
			}
		}
		data, err := os.ReadFile(bakPath)
		if err != nil {
			return res, fmt.Errorf("快照文件已丢失: %v", err)
		}
		if int64(len(data)) < meta.Size {
			return res, fmt.Errorf("快照文件不完整（期望 %d 字节，实得 %d），已中止回滚", meta.Size, len(data))
		}
		if err := os.MkdirAll(filepath.Dir(meta.Path), 0o755); err != nil {
			return res, err
		}
		if err := WriteFileAtomic(meta.Path, data, 0o644); err != nil {
			return res, err
		}
	} else {
		// 原文件是本次新建的，撤销即删除
		if err := os.Remove(meta.Path); err != nil && !os.IsNotExist(err) {
			return res, err
		}
	}
	_ = os.Remove(target.jsonPath)
	_ = os.Remove(target.bakPath)
	return res, nil
}

// DiscardLastSnapshot 丢弃最近一条快照（仅限当前会话）。
// 写盘失败时用：文件根本没变，留着这条快照只会让 /undo 出现"回退了但没区别"。
// 只挑当前会话的最新快照——此前会删掉别的会话的最新记录，
// 而那条记录本来是对方唯一的还原点。
func DiscardLastSnapshot() {
	snapMu.Lock()
	defer snapMu.Unlock()
	root := SnapshotsDir()
	entries := scanSnapshots(root)
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if !e.metaOK {
			continue
		}
		if e.meta.ConvID != "" && e.meta.ConvID != currentSnapConv {
			continue
		}
		_ = os.Remove(e.jsonPath)
		_ = os.Remove(e.bakPath)
		return
	}
}

// RestoreLatestFor 把某个文件恢复到它最近一次快照，并消耗掉那条快照。
// 用于"局部回滚"：一次操作里写了多个文件，中途失败时把已写的那几个退回去，
// 不留下半套改动，也不影响其他操作的历史快照。
func RestoreLatestFor(path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	snapMu.Lock()
	defer snapMu.Unlock()
	root := SnapshotsDir()
	entries := scanSnapshots(root)
	// 从新到旧找第一条属于该文件的快照
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if !e.metaOK {
			continue
		}
		meta := e.meta
		if !strings.EqualFold(meta.Path, abs) {
			continue
		}
		if meta.Existed {
			bakPath, err := bakPathFor(root, e)
			if err != nil {
				return false, err
			}
			if meta.Size > 0 {
				if st, err := os.Stat(bakPath); err != nil || st.Size() != meta.Size {
					return false, fmt.Errorf("快照文件损坏或不完整（.bak 大小与记录不符），已中止：%s", bakPath)
				}
			}
			data, rerr := os.ReadFile(bakPath)
			if rerr != nil {
				return false, fmt.Errorf("快照文件已丢失: %v", rerr)
			}
			if int64(len(data)) < meta.Size {
				return false, fmt.Errorf("快照文件不完整（期望 %d 字节，实得 %d），已中止", meta.Size, len(data))
			}
			if err := WriteFileAtomic(abs, data, 0o644); err != nil {
				return false, err
			}
		} else {
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				return false, err
			}
		}
		_ = os.Remove(e.jsonPath)
		_ = os.Remove(e.bakPath)
		return true, nil
	}
	return false, nil
}

// SnapshotCount 当前可回滚的步数。
func SnapshotCount() int {
	return len(scanSnapshots(SnapshotsDir()))
}

// bakSize 返回一条快照实际占用的 .bak 字节数（same_as 指向的不为本条计费）。
func bakSize(e snapshotEntry) int64 {
	if !e.metaOK || e.meta.SameAs != "" || e.meta.Oversize {
		return 0
	}
	if st, err := os.Stat(e.bakPath); err == nil {
		return st.Size()
	}
	return 0
}

// rewriteMeta 把一条快照的 meta 原子写回 .json（prune 时 re-anchor / promote 会改 same_as/指纹）。
func rewriteMeta(e snapshotEntry) error {
	body, err := json.Marshal(e.meta)
	if err != nil {
		return err
	}
	return WriteFileAtomic(e.jsonPath, body, 0o644)
}

// pruneSnapshots 双限保留：每会话最多 keep 条 + 全局 .bak 总字节 snapshotMaxTotalB。
//
// same_as 依赖处理：受害者若是其他条目的 same_as 目标（锚），不能直接删——
// 否则依赖者全部断链。做法是"锚提升"：挑出最新的一个依赖者，把锚的 .bak 复制给它
// （它成为新锚），其余依赖者的 same_as 改指向新锚。全链没有 dangling，也不丢撤销步数。
func pruneSnapshots(root string, keep int) {
	entries := scanSnapshots(root)
	index := make(map[string]int, len(entries)) // id → entries 下标
	for i, e := range entries {
		index[e.id] = i
	}

	victims := map[string]bool{}

	if len(entries) == 0 {
		// 没有 .json 时也要走后面的孤儿清理（纯 .bak 残留需要回收）
	} else {

		// —— 限 1：每个会话最多 keep 条（平铺根目录的旧快照算一个独立“会话桶”）——
		byConv := make(map[string][]int)
		for i, e := range entries {
			c := e.meta.ConvID
			if e.jsonPath == filepath.Join(root, e.id+".json") {
				c = "" // 平铺旧布局
			}
			byConv[c] = append(byConv[c], i)
		}
		for _, list := range byConv {
			if len(list) <= keep {
				continue
			}
			// entries 按 id 升序：前面的是最旧的，先淘汰
			for _, i := range list[:len(list)-keep] {
				victims[entries[i].id] = true
			}
		}

		// —— 限 2：全局 .bak 总字节 ——
		total := int64(0)
		for _, e := range entries {
			total += bakSize(e)
		}
		if total > snapshotMaxTotalB {
			for i := range entries {
				if total <= snapshotMaxTotalB {
					break
				}
				e := entries[i]
				if victims[e.id] {
					continue
				}
				victims[e.id] = true
				total -= bakSize(e)
			}
		}

		// —— same_as 锚提升：受害者是锚时，提升一个依赖者承接 .bak ——
		victimIDs := make([]string, 0, len(victims))
		for id := range victims {
			victimIDs = append(victimIDs, id)
		}
		sort.Strings(victimIDs) // 最旧（id 最小）先处理
		for _, vid := range victimIDs {
			if !victims[vid] {
				continue
			}
			vIdx := index[vid]
			v := entries[vIdx]
			// 收集依赖 v 的条目（当前内存视图，含前序处理已改写的 meta）
			var depIdx []int
			for j, e := range entries {
				if e.id != vid && e.metaOK && e.meta.SameAs == vid {
					depIdx = append(depIdx, j)
				}
			}
			if len(depIdx) > 0 {
				// 提升最新的依赖者作为新锚
				p := depIdx[len(depIdx)-1]
				promo := entries[p]
				// 锚的 .bak 复制为 promo 的 .bak
				if data, err := os.ReadFile(v.bakPath); err == nil {
					_ = WriteFileAtomic(promo.bakPath, data, 0o644)
					promo.meta.SameAs = ""
					if promo.meta.SHA256 == "" {
						promo.meta.SHA256 = v.meta.SHA256
					}
					promo.meta.Size = v.meta.Size
					_ = rewriteMeta(promo)
					entries[p] = promo
				}
				// promo 自身如果在受害者集里，把它摘掉（它现在是锚）
				delete(victims, promo.id)
				// 其余依赖者重新指向 promo
				for _, j := range depIdx[:len(depIdx)-1] {
					d := entries[j]
					d.meta.SameAs = promo.id
					_ = rewriteMeta(d)
					entries[j] = d
				}
			}
			_ = os.Remove(v.jsonPath)
			_ = os.Remove(v.bakPath)
			delete(victims, vid)
		}
	} // end else (entries 非空) 的 victims 计算与删除

	// —— 孤儿清理：没有对应 .json 的 .bak / .bak.tmp ——
	alive := make(map[string]bool, len(entries))
	for _, e := range entries {
		if _, err := os.Stat(e.jsonPath); err == nil {
			alive[e.id] = true
		}
	}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".bak.tmp") {
			_ = os.Remove(path)
			return nil
		}
		if !strings.HasSuffix(name, ".bak") {
			return nil
		}
		id := strings.TrimSuffix(name, ".bak")
		if !alive[id] {
			_ = os.Remove(path)
		}
		return nil
	})
}
