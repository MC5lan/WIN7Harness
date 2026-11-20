package codeidx

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"win7harness/internal/core"
)

// 造一个含若干 go 文件的临时仓库。
func memRepo(t *testing.T, files int) string {
	t.Helper()
	dir := t.TempDir()
	for i := 0; i < files; i++ {
		body := "package p\n\nfunc Fn" + string(rune('A'+i%26)) + "() {}\n\nfunc Other" + string(rune('A'+i%26)) + "() {}\n"
		sub := filepath.Join(dir, "pkg")
		os.MkdirAll(sub, 0o755)
		if err := os.WriteFile(filepath.Join(sub, "f"+string(rune('a'+i%26))+".go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// 低内存档必须收紧索引规模，否则 32 位进程容易在重建时 OOM。
func TestBuildLimitsFollowMemoryMode(t *testing.T) {
	defer core.SetLowMemory(false)
	core.SetLowMemory(false)
	// v1.29 起限额由内存档位决定，maxFiles/maxSymbols 只当天花板。
	// 这里断言"等于当前档位预算、且不超过天花板"，而不是写死 3000/20000
	// ——写死会让在 4G 开发机上跑测试时假失败。
	fStd, sStd := buildLimits()
	if m := core.MemProfile(); fStd != m.IndexFiles || sStd != m.IndexSymbols {
		t.Fatalf("标准档限额 = %d/%d，期望与 MemProfile 一致 %d/%d",
			fStd, sStd, m.IndexFiles, m.IndexSymbols)
	}
	if fStd > maxFiles || sStd > maxSymbols {
		t.Fatalf("限额 %d/%d 超过了天花板 %d/%d", fStd, sStd, maxFiles, maxSymbols)
	}
	core.SetLowMemory(true)
	fLow, sLow := buildLimits()
	if fLow >= fStd || sLow >= sStd {
		t.Fatalf("低内存档应收紧：%d/%d vs %d/%d", fLow, sLow, fStd, sStd)
	}
	if fLow != core.MemProfile().IndexFiles || sLow != core.MemProfile().IndexSymbols {
		t.Fatal("低内存档限额应与 MemProfile 一致")
	}
}

// 全仓未变时应直接复用旧快照：既省时间，也避免新旧两份符号表同时驻留。
func TestBuildReusesUnchangedSnapshot(t *testing.T) {
	dir := memRepo(t, 5)
	core.SetLowMemory(false)
	first := Build(dir, nil)
	if len(first.Syms) == 0 {
		t.Fatal("首次构建应有符号")
	}
	takeDirty() // 清掉首次构建可能残留的脏标记
	second := Build(dir, first)
	if second != first {
		t.Fatal("文件未变时应复用同一份快照")
	}
	if second.LastBuild.Reused != second.LastBuild.Total {
		t.Fatalf("应全部复用，实际 %+v", second.LastBuild)
	}
	if !second.LastBuild.Incremental {
		t.Fatal("复用路径应标记为增量")
	}
}

// 有脏文件时必须真的重新解析，不能偷懒复用。
func TestBuildParsesDirtyFile(t *testing.T) {
	dir := memRepo(t, 3)
	core.SetLowMemory(false)
	first := Build(dir, nil)
	rel := "pkg/fa.go"
	MarkDirtyRel(dir, rel)
	second := Build(dir, first)
	if second == first {
		t.Fatal("存在脏文件时不应走全量复用路径")
	}
	if second.LastBuild.Dirty == 0 {
		t.Fatalf("应记录脏文件数：%+v", second.LastBuild)
	}
}

// 内存估算要随规模增长，且与预算字段一同回填，便于 /index 显示峰值。
func TestEstimateBytesAndBudget(t *testing.T) {
	core.SetLowMemory(false)
	small := Build(memRepo(t, 2), nil)
	big := Build(memRepo(t, 12), nil)
	if small.EstimateBytes() <= 0 || big.EstimateBytes() <= 0 {
		t.Fatal("内存估算不应为 0")
	}
	if big.EstimateBytes() <= small.EstimateBytes() {
		t.Fatalf("大仓库估算应更大：%d vs %d", big.EstimateBytes(), small.EstimateBytes())
	}
	_, symLimit := buildLimits()
	want := int64(symLimit) * symOverhead
	if big.LastBuild.BudgetBytes != want {
		t.Fatalf("预算 = %d，期望 %d", big.LastBuild.BudgetBytes, want)
	}
	if big.LastBuild.MemBytes != big.EstimateBytes() {
		t.Fatal("Stats.MemBytes 应与 EstimateBytes 一致")
	}
}

func TestInternStr(t *testing.T) {
	pool := map[string]string{}
	a := internStr(pool, "pkg/a.go")
	b := internStr(pool, "pkg/a.go")
	if a != b {
		t.Fatal("驻留后应得到相同内容")
	}
	if unsafe.StringData(a) != unsafe.StringData(b) {
		t.Fatal("驻留后应共享同一份底层字符串")
	}
	if internStr(pool, "") != "" {
		t.Fatal("空串应原样返回")
	}
}

// 驻留不能改变索引语义：同一文件的多个符号仍归到同一 File，且底层字符串共享。
func TestSymbolFileInterning(t *testing.T) {
	dir := memRepo(t, 4)
	core.SetLowMemory(false)
	ix := Build(dir, nil)
	byFile := map[string][]Symbol{}
	for _, s := range ix.Syms {
		byFile[s.File] = append(byFile[s.File], s)
	}
	if len(byFile) == 0 {
		t.Fatal("应至少索引到一个文件")
	}
	for f, syms := range byFile {
		if len(syms) < 2 {
			continue
		}
		first := unsafe.StringData(syms[0].File)
		for _, s := range syms[1:] {
			if unsafe.StringData(s.File) != first {
				t.Fatalf("文件 %s 的符号未共享同一份字符串", f)
			}
		}
	}
}
