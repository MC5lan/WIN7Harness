package core

// 项目约定沉淀：自动探测工程类型与构建/测试命令，写进 AGENTS.md 的
// 受控区块（harness:auto 标记内），用户手写内容一律不动。
// 重复执行结果一致（幂等），不会在文件里堆出十几段重复约定。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	agentsAutoBegin = "<!-- harness:auto:begin -->"
	agentsAutoEnd   = "<!-- harness:auto:end -->"
)

// ProjectProfile 工程画像。
type ProjectProfile struct {
	Lang     string
	BuildCmd string
	TestCmd  string
	RunCmd   string
	Dirs     []string
	Files    []string
}

// DetectProject 依据特征文件推断工程类型与常用命令。只读。
func DetectProject(dir string) ProjectProfile {
	p := ProjectProfile{}
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	switch {
	case has("go.mod"):
		p.Lang = "Go"
		p.BuildCmd = "go build ./..."
		p.TestCmd = "go test ./..."
		p.RunCmd = "go run ."
	case has("package.json"):
		p.Lang = "Node.js"
		p.BuildCmd = "npm run build"
		p.TestCmd = "npm test"
		p.RunCmd = "npm start"
	case has("Cargo.toml"):
		p.Lang = "Rust"
		p.BuildCmd = "cargo build"
		p.TestCmd = "cargo test"
		p.RunCmd = "cargo run"
	case has("pyproject.toml"), has("requirements.txt"), has("setup.py"):
		p.Lang = "Python"
		p.BuildCmd = "python -m build"
		p.TestCmd = "python -m pytest"
		p.RunCmd = "python main.py"
	case has("pom.xml"):
		p.Lang = "Java (Maven)"
		p.BuildCmd = "mvn -q package"
		p.TestCmd = "mvn -q test"
	case has("build.gradle"), has("build.gradle.kts"):
		p.Lang = "Java (Gradle)"
		p.BuildCmd = "gradle build"
		p.TestCmd = "gradle test"
	}
	if has("Makefile") {
		if p.BuildCmd == "" {
			p.BuildCmd = "make"
		}
		if p.TestCmd == "" {
			p.TestCmd = "make test"
		}
	}
	if p.Lang == "" {
		p.Lang = "未识别"
	}

	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			n := e.Name()
			if strings.HasPrefix(n, ".") || n == "node_modules" || n == "vendor" {
				continue
			}
			if e.IsDir() {
				p.Dirs = append(p.Dirs, n)
			} else if isSourceName(n) {
				p.Files = append(p.Files, n)
			}
		}
	}
	sort.Strings(p.Dirs)
	sort.Strings(p.Files)
	if len(p.Dirs) > 8 {
		p.Dirs = p.Dirs[:8]
	}
	if len(p.Files) > 8 {
		p.Files = p.Files[:8]
	}
	return p
}

func isSourceName(n string) bool {
	low := strings.ToLower(n)
	for _, ext := range []string{".go", ".py", ".js", ".ts", ".rs", ".java", ".md"} {
		if strings.HasSuffix(low, ext) {
			return true
		}
	}
	return false
}

// AgentsAutoBlock 生成自动区块内容（不含时间戳，保证幂等比较）。
func AgentsAutoBlock(p ProjectProfile) string {
	var b strings.Builder
	b.WriteString(agentsAutoBegin + "\n")
	b.WriteString("## 项目约定（自动生成，可手改但下次会覆盖本区块）\n\n")
	fmt.Fprintf(&b, "- 语言 / 技术栈：%s\n", p.Lang)
	if p.BuildCmd != "" {
		fmt.Fprintf(&b, "- 构建命令：%s\n", "`"+p.BuildCmd+"`")
	}
	if p.TestCmd != "" {
		fmt.Fprintf(&b, "- 测试命令：%s\n", "`"+p.TestCmd+"`")
	}
	if p.RunCmd != "" {
		fmt.Fprintf(&b, "- 运行命令：%s\n", "`"+p.RunCmd+"`")
	}
	if len(p.Dirs) > 0 {
		fmt.Fprintf(&b, "- 主要目录：%s\n", strings.Join(p.Dirs, "、"))
	}
	if len(p.Files) > 0 {
		fmt.Fprintf(&b, "- 根目录文件：%s\n", strings.Join(p.Files, "、"))
	}
	b.WriteString("- 提交前请先跑测试命令，失败不得提交。\n")
	b.WriteString(agentsAutoEnd)
	return b.String()
}

// UpsertAgentsContract 把自动区块写进 AGENTS.md：
// 已有区块则原位替换，没有则追加到末尾。返回写入路径与是否发生变化。
func UpsertAgentsContract(dir string) (string, bool, error) {
	path := filepath.Join(dir, "AGENTS.md")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return path, false, err
	}
	content := string(old)
	block := AgentsAutoBlock(DetectProject(dir))

	if s := strings.Index(content, agentsAutoBegin); s >= 0 {
		if e := strings.Index(content[s:], agentsAutoEnd); e >= 0 {
			end := s + e + len(agentsAutoEnd)
			exists := content[s:end]
			if strings.TrimSpace(exists) == strings.TrimSpace(block) {
				return path, false, nil
			}
			content = content[:s] + block + content[end:]
			return path, true, writeAgents(path, content)
		}
	}
	if strings.TrimSpace(content) == "" {
		content = fmt.Sprintf("# %s 项目约定\n\n> 由 WIN7 Harness 维护；手写内容请放在自动区块之外。\n\n%s\n", filepath.Base(dir), block)
	} else {
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + block + "\n"
	}
	return path, true, writeAgents(path, content)
}

func writeAgents(path, content string) error {
	tmp := path + ".tmp"
	if err := WriteFileAtomic(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
