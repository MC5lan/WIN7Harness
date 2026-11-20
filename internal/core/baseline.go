package core

// 覆盖率与性能基线：解析 go test 的 coverprofile / benchmark 输出，
// 与本地基线对比给出回归结论。全部为纯函数 + 一次落盘，便于在 Win7 单测里跑。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BenchCase 一条基准测试结果。
type BenchCase struct {
	Name    string  `json:"name"`
	NsPerOp float64 `json:"ns_per_op"`
	AllocB  float64 `json:"alloc_b"`
	Allocs  float64 `json:"allocs"`
}

// BenchBaseline 基线文件。
type BenchBaseline struct {
	Version   string               `json:"version"`
	CreatedAt string               `json:"created_at"`
	Cases     map[string]BenchCase `json:"cases"`
}

// BaselinePath 返回性能基线文件路径。
func BaselinePath() string { return filepath.Join(DataDir(), "bench-baseline.json") }

// ParseBenchOutput 解析 `go test -bench . -benchmem` 的输出。
// 只认以 Benchmark 开头的行，其余（PASS/ok/自定义日志）一律跳过。
func ParseBenchOutput(s string) []BenchCase {
	var out []BenchCase
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(strings.TrimRight(ln, "\r"))
		if !strings.HasPrefix(ln, "Benchmark") {
			continue
		}
		f := strings.Fields(ln)
		if len(f) < 3 {
			continue
		}
		c := BenchCase{Name: f[0]}
		ok := false
		for i := 1; i+1 < len(f); i++ {
			v, err := strconv.ParseFloat(f[i], 64)
			if err != nil {
				continue
			}
			switch f[i+1] {
			case "ns/op":
				c.NsPerOp, ok = v, true
			case "B/op":
				c.AllocB = v
			case "allocs/op":
				c.Allocs = v
			}
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
}

// LoadBenchBaseline 读取基线文件。
func LoadBenchBaseline(path string) (*BenchBaseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b BenchBaseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("基线文件损坏：%v", err)
	}
	if b.Cases == nil {
		b.Cases = map[string]BenchCase{}
	}
	return &b, nil
}

// SaveBenchBaseline 写入基线文件。
func SaveBenchBaseline(path string, b *BenchBaseline) error {
	if b.Cases == nil {
		b.Cases = map[string]BenchCase{}
	}
	if b.Version == "" {
		b.Version = Version
	}
	if b.CreatedAt == "" {
		b.CreatedAt = time.Now().Format("2006-01-02 15:04:05")
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := WriteFileAtomic(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// BenchDelta 一条对比结论。
type BenchDelta struct {
	Name   string
	Base   float64
	Cur    float64
	Pct    float64 // 正值表示变慢
	Status string  // regress | improve | same | new | gone
}

// benchTolerance 超过该比例（%）才算回归，避免噪声误报。
const benchTolerance = 20.0

// CompareBench 对比基线与本次结果。cur 中同名用例与 base 比较，
// 变慢超过 benchTolerance 记 regress；变快同等幅度记 improve。
func CompareBench(base map[string]BenchCase, cur []BenchCase) []BenchDelta {
	var out []BenchDelta
	seen := map[string]bool{}
	for _, c := range cur {
		seen[c.Name] = true
		b, ok := base[c.Name]
		if !ok {
			out = append(out, BenchDelta{Name: c.Name, Cur: c.NsPerOp, Status: "new"})
			continue
		}
		d := BenchDelta{Name: c.Name, Base: b.NsPerOp, Cur: c.NsPerOp, Status: "same"}
		if b.NsPerOp > 0 {
			d.Pct = (c.NsPerOp - b.NsPerOp) / b.NsPerOp * 100
			switch {
			case d.Pct > benchTolerance:
				d.Status = "regress"
			case d.Pct < -benchTolerance:
				d.Status = "improve"
			}
		}
		out = append(out, d)
	}
	for name, b := range base {
		if !seen[name] {
			out = append(out, BenchDelta{Name: name, Base: b.NsPerOp, Status: "gone"})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		rank := func(s string) int {
			switch s {
			case "regress":
				return 0
			case "improve":
				return 1
			case "new":
				return 2
			case "gone":
				return 3
			}
			return 4
		}
		if rank(out[i].Status) != rank(out[j].Status) {
			return rank(out[i].Status) < rank(out[j].Status)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// FormatBenchDeltas 把对比结论渲染成文本。
func FormatBenchDeltas(ds []BenchDelta) string {
	if len(ds) == 0 {
		return "没有可对比的基准数据。"
	}
	var b strings.Builder
	regress := 0
	for _, d := range ds {
		switch d.Status {
		case "regress":
			regress++
			fmt.Fprintf(&b, "回归  %s  %.0f → %.0f ns/op (%+.0f%%)\n", d.Name, d.Base, d.Cur, d.Pct)
		case "improve":
			fmt.Fprintf(&b, "变快  %s  %.0f → %.0f ns/op (%+.0f%%)\n", d.Name, d.Base, d.Cur, d.Pct)
		case "new":
			fmt.Fprintf(&b, "新增  %s  %.0f ns/op（无基线）\n", d.Name, d.Cur)
		case "gone":
			fmt.Fprintf(&b, "消失  %s  基线 %.0f ns/op 本次未跑\n", d.Name, d.Base)
		default:
			fmt.Fprintf(&b, "持平  %s  %.0f ns/op (%+.0f%%)\n", d.Name, d.Cur, d.Pct)
		}
	}
	fmt.Fprintf(&b, "\n共 %d 项，回归 %d 项（阈值 %.0f%%）", len(ds), regress, benchTolerance)
	return b.String()
}

// CoverStat 覆盖率统计。
type CoverStat struct {
	Package string
	Total   int
	Covered int
	Percent float64
}

// ParseCoverProfile 解析 go test -coverprofile 生成的文本。
// 每行形如：pkg/file.go:10.2,12.8 3 1（语句数 命中次数）。
func ParseCoverProfile(data string) (CoverStat, []CoverStat) {
	total := map[string]*CoverStat{}
	var order []string
	var all CoverStat
	for _, ln := range strings.Split(data, "\n") {
		ln = strings.TrimSpace(strings.TrimRight(ln, "\r"))
		if ln == "" || strings.HasPrefix(ln, "mode:") {
			continue
		}
		f := strings.Fields(ln)
		if len(f) < 3 {
			continue
		}
		loc := f[0]
		colon := strings.LastIndex(loc, ":")
		if colon <= 0 {
			continue
		}
		file := loc[:colon]
		stmts, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		hits, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		pkg := filepath.ToSlash(filepath.Dir(file))
		if pkg == "." {
			pkg = "(root)"
		}
		st, ok := total[pkg]
		if !ok {
			st = &CoverStat{Package: pkg}
			total[pkg] = st
			order = append(order, pkg)
		}
		st.Total += stmts
		if hits > 0 {
			st.Covered += stmts
		}
		all.Total += stmts
		if hits > 0 {
			all.Covered += stmts
		}
	}
	var out []CoverStat
	for _, p := range order {
		st := total[p]
		if st.Total > 0 {
			st.Percent = float64(st.Covered) / float64(st.Total) * 100
		}
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Percent < out[j].Percent })
	all.Package = "全部"
	if all.Total > 0 {
		all.Percent = float64(all.Covered) / float64(all.Total) * 100
	}
	return all, out
}

// FormatCoverage 渲染覆盖率报告，低覆盖包单独列出。
func FormatCoverage(all CoverStat, pkgs []CoverStat, lowThreshold float64) string {
	if all.Total == 0 {
		return "覆盖率数据为空（可能是没有任何可统计语句，或测试未运行）。"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "总覆盖率：%.1f%%（%d/%d 语句）\n", all.Percent, all.Covered, all.Total)
	var low []string
	for _, p := range pkgs {
		if p.Percent < lowThreshold {
			low = append(low, fmt.Sprintf("%s %.1f%%", p.Package, p.Percent))
		}
	}
	if len(pkgs) > 0 {
		b.WriteString("\n按包（由低到高，最多 10 个）：\n")
		for i, p := range pkgs {
			if i >= 10 {
				fmt.Fprintf(&b, "…另有 %d 个包\n", len(pkgs)-10)
				break
			}
			fmt.Fprintf(&b, "  %-40s %5.1f%%  (%d/%d)\n", p.Package, p.Percent, p.Covered, p.Total)
		}
	}
	if len(low) > 0 {
		fmt.Fprintf(&b, "\n低于 %.0f%% 的包 %d 个，优先补测试：\n  %s\n", lowThreshold, len(low), strings.Join(low, "\n  "))
	}
	return b.String()
}
