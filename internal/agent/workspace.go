package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// workspaceSummary 生成一段很短的“仓库第一眼”信息。
//
// Codex/Claude Code 的工程感很大一部分来自“先看仓库再动手”。这里只列顶层
// 条目和项目标记文件，不做递归扫描，既省 token，也避免模型一上来就 list_dir
// 或全盘搜索。真正的细节仍由 list_dir / glob / code_search 提供。
func workspaceSummary(workDir string) string {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return ""
	}
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return ""
	}
	skip := map[string]bool{
		".git": true, ".svn": true, ".hg": true, "node_modules": true,
		"vendor": true, "dist": true, "build": true, ".idea": true, ".vscode": true,
	}
	var dirs, files []string
	for _, e := range entries {
		name := e.Name()
		if skip[name] {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, name+"/")
		} else {
			files = append(files, name)
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)
	items := append(dirs, files...)
	if len(items) > 30 {
		items = append(items[:30], "…")
	}
	if len(items) == 0 {
		items = []string{"（空目录）"}
	}

	var b strings.Builder
	b.WriteString("\n## 工作区概览\n")
	fmt.Fprintf(&b, "顶层: %s\n", strings.Join(items, "  "))
	if marker := projectMarker(workDir); marker != "" {
		b.WriteString("项目标记: " + marker + "\n")
	}
	return b.String()
}

func projectMarker(workDir string) string {
	type marker struct {
		file string
		name string
	}
	markers := []marker{
		{"go.mod", "Go"},
		{"package.json", "Node/JS"},
		{"pyproject.toml", "Python"},
		{"requirements.txt", "Python"},
		{"Cargo.toml", "Rust"},
		{"Makefile", "Make"},
		{"CMakeLists.txt", "CMake"},
	}
	var found []string
	for _, m := range markers {
		p := filepath.Join(workDir, m.file)
		if _, err := os.Stat(p); err == nil {
			found = append(found, m.file+"（"+m.name+"）")
		}
	}
	if len(found) == 0 {
		return ""
	}
	// go.mod 的 module 名对写包路径很有用，只读前两行，不整文件朗读。
	if b, err := os.ReadFile(filepath.Join(workDir, "go.mod")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") {
				found = append(found, "module="+strings.TrimSpace(strings.TrimPrefix(line, "module ")))
				break
			}
		}
	}
	return strings.Join(found, "；")
}
