package agent

import (
	"os"
	"path/filepath"

	"win7harness/internal/core"
)

// MergeFixtureRun 自检用的一份子任务改动：描述 + 相对路径到文件内容。
type MergeFixtureRun struct {
	Desc    string
	Changed map[string]string
}

// MergeFixture 用给定的改动集合模拟"多个子任务各写各的副本再合并"，
// 返回合并报告。仅供自检与测试使用，正常路径走 SpawnBatch。
func MergeFixture(root string, runs []MergeFixtureRun) (MergeReport, error) {
	var built []*isolatedRun
	var dirs []string
	defer func() {
		for _, d := range dirs {
			os.RemoveAll(d)
		}
	}()
	for i, r := range runs {
		dir, err := os.MkdirTemp(root, ".mergefix-")
		if err != nil {
			return MergeReport{}, err
		}
		dirs = append(dirs, dir)
		run := &isolatedRun{Desc: r.Desc, Dir: dir}
		// 按固定顺序建文件，避免 map 遍历顺序影响"谁先胜出"的断言
		paths := make([]string, 0, len(r.Changed))
		for rel := range r.Changed {
			paths = append(paths, rel)
		}
		for _, rel := range sortedStrings(paths) {
			p := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return MergeReport{}, err
			}
			if err := core.WriteFileAtomic(p, []byte(r.Changed[rel]), 0o644); err != nil {
				return MergeReport{}, err
			}
			run.Changed = append(run.Changed, rel)
		}
		_ = i
		built = append(built, run)
	}
	return MergeWrites(root, built), nil
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
