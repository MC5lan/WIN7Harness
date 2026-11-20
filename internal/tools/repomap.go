package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"win7harness/internal/core"
)

func init() {
	register(Tool{
		Name: "repo_map",
		Desc: "轻量仓库地图：列出 Go module、顶层条目、包含源码的目录（文件数/测试数）、main 入口和项目标记。大仓库先看它再决定读哪些文件，比 list_dir 逐层翻更快、更省 token。",
		Schema: `{
  "type":"object",
  "properties":{
    "path":{"type":"string","description":"仓库根目录，默认当前工作目录"}
  }
}`,
		Run: runRepoMap,
	})
}

const (
	repoMapMaxFiles = 5000
	repoMapMaxDirs  = 60
)

type repoDirInfo struct {
	rel    string
	files  int
	tests  int
	goMain bool
}

func runRepoMap(ctx *Context, args map[string]interface{}) (Result, error) {
	root := core.ResolvePath(ctx.WorkDir, Get(args, "path"))
	if root == "" {
		root = ctx.WorkDir
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return Result{}, fmt.Errorf("目录不存在：%s", root)
	}
	if err := guardSearchRoot(root); err != nil {
		return Result{}, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "仓库：%s\n", root)
	if mod := goModuleName(root); mod != "" {
		fmt.Fprintf(&b, "Go module：%s\n", mod)
	}
	if markers := projectMarkerNames(root); len(markers) > 0 {
		fmt.Fprintf(&b, "项目标记：%s\n", strings.Join(markers, "、"))
	}
	if tops := topEntries(root, 30); len(tops) > 0 {
		fmt.Fprintf(&b, "顶层：%s\n", strings.Join(tops, "  "))
	}

	dirs := map[string]*repoDirInfo{}
	totalFiles := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			if path != root && skipWalkDir(d.Name(), path, root) {
				return filepath.SkipDir
			}
			return nil
		}
		totalFiles++
		if totalFiles > repoMapMaxFiles {
			return errWalkBudget
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		dir := filepath.ToSlash(filepath.Dir(rel))
		if dir == "." {
			dir = "."
		}
		info := dirs[dir]
		if info == nil {
			info = &repoDirInfo{rel: dir}
			dirs[dir] = info
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".go" {
			info.files++
			if strings.HasSuffix(path, "_test.go") {
				info.tests++
			}
			if strings.HasSuffix(rel, "/main.go") || rel == "main.go" {
				info.goMain = true
			}
		}
		return nil
	})

	list := make([]*repoDirInfo, 0, len(dirs))
	for _, d := range dirs {
		if d.files > 0 || d.goMain {
			list = append(list, d)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].files != list[j].files {
			return list[i].files > list[j].files
		}
		return list[i].rel < list[j].rel
	})
	if len(list) > repoMapMaxDirs {
		list = list[:repoMapMaxDirs]
	}
	b.WriteString("源码目录（Go）：\n")
	if len(list) == 0 {
		b.WriteString("  （未发现 .go 文件）\n")
	}
	for _, d := range list {
		entry := "  " + d.rel
		if d.goMain {
			entry += "  [main]"
		}
		fmt.Fprintf(&b, "%s  %d 个 .go（%d 个测试）\n", entry, d.files, d.tests)
	}
	if totalFiles > repoMapMaxFiles {
		fmt.Fprintf(&b, "…文件超过 %d 个，地图已截断；请用 code_search / grep 缩小范围。\n", repoMapMaxFiles)
	}
	return Result{Output: b.String(), Title: "仓库地图"}, nil
}

func goModuleName(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

func projectMarkerNames(root string) []string {
	names := []string{"go.mod", "package.json", "pyproject.toml", "requirements.txt", "Cargo.toml", "Makefile", "CMakeLists.txt"}
	var out []string
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(root, n)); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func topEntries(root string, limit int) []string {
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if len(out) >= limit {
			out = append(out, "…")
			break
		}
		if e.IsDir() {
			if skipWalkDir(e.Name(), filepath.Join(root, e.Name()), root) {
				continue
			}
			out = append(out, e.Name()+"/")
		} else {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
