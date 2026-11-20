package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 快照存储重构（嵌套布局 + 分片 + 去重 + 双限保留）的专项用例。
// 注意：测试进程内 currentSnapConv 是全局量，本文件全部用例显式 SetCurrentConv，
// 且不要在并行测试里跑。

func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", home)
	return home
}

func snapEntries(t *testing.T) []snapshotEntry {
	t.Helper()
	return scanSnapshots(SnapshotsDir())
}

func TestSnapshotNestedLayoutAndUndo(t *testing.T) {
	withTempHome(t)
	SetCurrentConv("conv1234")
	f := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(f, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotFile(f); err != nil {
		t.Fatal(err)
	}
	// 新布局：<home>/snapshots/conv1234/<yyyymm>/<id>.json
	entries := snapEntries(t)
	if len(entries) != 1 {
		t.Fatalf("应有 1 条快照，实际 %d", len(entries))
	}
	wantDir := filepath.Join(SnapshotsDir(), "conv1234", time.Now().Format("200601"))
	if filepath.Dir(entries[0].jsonPath) != wantDir {
		t.Fatalf("快照落盘目录错误：%s（期望 %s）", entries[0].jsonPath, wantDir)
	}
	// 修改后 Undo 回原内容
	if err := os.WriteFile(f, []byte("v2-改过"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := UndoLast()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(f)
	if string(got) != "v1" {
		t.Fatalf("撤销后内容应为 v1，实际 %q", got)
	}
	_ = res
	if n := SnapshotCount(); n != 0 {
		t.Fatalf("撤销后应无快照，实际 %d", n)
	}
}

func TestSnapshotSameAsDedup(t *testing.T) {
	withTempHome(t)
	SetCurrentConv("convsame")
	f := filepath.Join(t.TempDir(), "same.txt")
	if err := os.WriteFile(f, []byte("相同的内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotFile(f); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotFile(f); err != nil {
		t.Fatal(err)
	}
	entries := snapEntries(t)
	if len(entries) != 2 {
		t.Fatalf("应有 2 条快照，实际 %d", len(entries))
	}
	// 第二条（较新的）必须 same_as 指向第一条，且不自带 .bak
	sameAs := 0
	baks := 0
	for _, e := range entries {
		if e.meta.SameAs != "" {
			sameAs++
			if _, err := os.Stat(e.bakPath); !os.IsNotExist(err) {
				t.Fatalf("same_as 条目不应自带 .bak：%s", e.bakPath)
			}
			continue
		}
		if _, err := os.Stat(e.bakPath); err == nil {
			baks++
		}
	}
	if sameAs != 1 || baks != 1 {
		t.Fatalf("去重失败：same_as=%d baks=%d", sameAs, baks)
	}
	// 连续两次 Undo 都应拿回原文（链解析）
	for i := 0; i < 2; i++ {
		if _, err := UndoLast(); err != nil {
			t.Fatalf("第 %d 次撤销失败：%v", i+1, err)
		}
		got, _ := os.ReadFile(f)
		if string(got) != "相同的内容" {
			t.Fatalf("第 %d 次撤销后内容应为原文，实际 %q", i+1, got)
		}
	}
}

func TestSnapshotLegacyFlatReadable(t *testing.T) {
	withTempHome(t)
	SetCurrentConv("")
	f := filepath.Join(t.TempDir(), "legacy.txt")
	if err := os.WriteFile(f, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := SnapshotsDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// 手工写一条旧平铺快照
	id := "00000000000000000001_000001"
	bak := filepath.Join(root, id+".bak")
	if err := os.WriteFile(bak, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	meta := fmt.Sprintf(`{"path":%q,"existed":true,"at":"2026-11-10T10:00:00Z","size":3}`, filepath.ToSlash(f))
	if err := os.WriteFile(filepath.Join(root, id+".json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte("new-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UndoLast(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(f)
	if string(got) != "old" {
		t.Fatalf("旧平铺快照应能回滚，实际 %q", got)
	}
}

func TestSnapshotPrunePerConvRetention(t *testing.T) {
	withTempHome(t)
	SetCurrentConv("conva")
	f := filepath.Join(t.TempDir(), "prune.txt")
	for i := 0; i < snapshotRetain+5; i++ {
		if err := os.WriteFile(f, []byte(fmt.Sprintf("v%03d", i)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := SnapshotFile(f); err != nil {
			t.Fatal(err)
		}
	}
	entries := snapEntries(t)
	countA := 0
	for _, e := range entries {
		if e.meta.ConvID == "conva" {
			countA++
		}
	}
	if countA != snapshotRetain {
		t.Fatalf("convA 应保留 %d 条，实际 %d", snapshotRetain, countA)
	}
	// 另一个会话的快照不受影响
	SetCurrentConv("convb")
	fb := filepath.Join(t.TempDir(), "b.txt")
	if err := os.WriteFile(fb, []byte("b0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotFile(fb); err != nil {
		t.Fatal(err)
	}
	countB := 0
	for _, e := range snapEntries(t) {
		if e.meta.ConvID == "convb" {
			countB++
		}
	}
	if countB != 1 {
		t.Fatalf("convB 快照应保留 1 条，实际 %d", countB)
	}
}

func TestSnapshotPruneOrphanBak(t *testing.T) {
	withTempHome(t)
	root := SnapshotsDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, "00000000000000000001_000001.bak")
	if err := os.WriteFile(orphan, []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	pruneSnapshots(root, snapshotRetain)
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("孤儿 .bak 应被清理")
	}
}

func TestSnapshotSameAsChainBrokenAborts(t *testing.T) {
	withTempHome(t)
	SetCurrentConv("convbad")
	f := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(f, []byte("orig"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 手工构造一条 same_as 指向不存在目标的快照（模拟“指向的旧快照已被 prune”）
	dir := snapWriteDir(SnapshotsDir(), "convbad", time.Now())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	id := snapshotID()
	meta := snapshotMeta{
		Path:    f,
		Existed: true,
		At:      time.Now().Format(time.RFC3339),
		ConvID:  "convbad",
		Size:    4,
		SameAs:  "00000000000000000001_000001",
	}
	body, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, id+".json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UndoLast(); err == nil {
		t.Fatal("断链 same_as 应报错而不是静默成功")
	}
	got, _ := os.ReadFile(f)
	if string(got) != "orig" {
		t.Fatalf("断链时文件不应被改动，实际 %q", got)
	}
}

func TestSnapshotOversizeKeepsFingerprintOnly(t *testing.T) {
	withTempHome(t)
	SetCurrentConv("convbig")
	f := filepath.Join(t.TempDir(), "big.bin")
	buf := make([]byte, snapshotMaxFileB+1)
	if err := os.WriteFile(f, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotFile(f); err != nil {
		t.Fatal(err)
	}
	entries := snapEntries(t)
	if len(entries) != 1 {
		t.Fatalf("应有 1 条快照，实际 %d", len(entries))
	}
	e := entries[0]
	if !e.meta.Oversize {
		t.Fatal("超大文件应标记 oversize")
	}
	if _, err := os.Stat(e.bakPath); !os.IsNotExist(err) {
		t.Fatal("oversize 条目不应有 .bak")
	}
	if _, err := UndoLast(); err == nil {
		t.Fatal("oversize 撤销应明确报错（不写半截内容）")
	}
}

func TestSnapshotSameAsAnchorPromotedOnPrune(t *testing.T) {
	withTempHome(t)
	SetCurrentConv("convdup")
	f := filepath.Join(t.TempDir(), "dup.txt")
	if err := os.WriteFile(f, []byte("constant"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 全部同内容：1 条锚（带 .bak）+ 其余 same_as。
	for i := 0; i < snapshotRetain+3; i++ {
		if err := SnapshotFile(f); err != nil {
			t.Fatal(err)
		}
	}
	entries := snapEntries(t)
	count := 0
	ali := map[string]bool{}
	for _, e := range entries {
		ali[e.id] = true
		count++
	}
	if count > snapshotRetain {
		t.Fatalf("保留 %d 条超过上限 %d", count, snapshotRetain)
	}
	// 关键：任何 same_as 都不能 dangling（锚提升生效）
	for _, e := range entries {
		if e.meta.SameAs != "" && !ali[e.meta.SameAs] {
			t.Fatalf("断链：%s 的 same_as 指向 %s 但目标已不存在", e.id, e.meta.SameAs)
		}
	}
	// 至少有一条带 .bak 的锚（内容还可恢复）
	anchors := 0
	for _, e := range entries {
		if e.meta.SameAs == "" && e.meta.Existed && !e.meta.Oversize {
			if st, err := os.Stat(e.bakPath); err == nil && st.Size() == int64(len("constant")) {
				anchors++
			}
		}
	}
	if anchors == 0 {
		t.Fatal("锚提升失败：没有可恢复 .bak 的锚")
	}
	// 随机抽一条撤销：内容必须回到 constant
	if _, err := UndoLast(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(f)
	if string(got) != "constant" {
		t.Fatalf("撤销后内容应为 constant，实际 %q", got)
	}
}
