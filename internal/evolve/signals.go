package evolve

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CollectSignals 从仓库里收集低成本改进信号：
// PROGRESS 的待办段、TODO/FIXME、以及最近的门禁失败（由调用方追加）。
func CollectSignals(ctx context.Context, dir string) []string {
	var out []string
	out = append(out, readProgressTodos(dir)...)
	out = append(out, gitTodos(ctx, dir)...)
	return out
}

// readProgressTodos 抽取 PROGRESS.md 里“待办”段落的条目。
func readProgressTodos(dir string) []string {
	raw, err := os.ReadFile(filepath.Join(dir, "PROGRESS.md"))
	if err != nil {
		return nil
	}
	lines := strings.Split(string(raw), "\n")
	in := false
	var out []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "## ") {
			in = strings.Contains(t, "待办")
			continue
		}
		if !in || t == "" {
			continue
		}
		t = strings.TrimLeft(t, "-*0123456789.、) ")
		if t == "" {
			continue
		}
		out = append(out, "PROGRESS 待办："+clip(t, 240))
		if len(out) >= 12 {
			break
		}
	}
	return out
}

// gitTodos 用 git grep 找 TODO/FIXME，限制条数，避免把整棵树塞进提示词。
func gitTodos(ctx context.Context, dir string) []string {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", "-C", dir, "grep", "-n", "-E", "TODO|FIXME")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var res []string
	for _, ln := range strings.Split(string(out), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		res = append(res, "代码标记："+clip(ln, 240))
		if len(res) >= 20 {
			break
		}
	}
	return res
}
