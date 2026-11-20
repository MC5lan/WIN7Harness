package codeidx

import (
	"os"
	"path/filepath"
	"testing"
)

func writeF(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sampleRepo(t *testing.T) string {
	root := t.TempDir()
	writeF(t, root, "internal/app/engine.go", `package app

import "fmt"

const maxRounds = 8

var ErrEmpty = fmt.Errorf("empty")

type Runner struct {
	Name string
}

type Doer interface {
	Do() error
}

func (r *Runner) RunTurn(n int) (string, error) {
	return "", nil
}

func parseConfig(path string) *Runner {
	return &Runner{Name: path}
}
`)
	writeF(t, root, "scripts/tool.py", `import os

class Fetcher:
    def fetch_all(self, url):
        return url

def main():
    pass
`)
	writeF(t, root, "node_modules/x/index.js", `function shouldBeSkipped() {}`)
	return root
}

func TestBuildGoSymbols(t *testing.T) {
	root := sampleRepo(t)
	ix := Build(root, nil)
	byName := map[string]Symbol{}
	for _, s := range ix.Syms {
		byName[s.Name] = s
	}
	if _, ok := byName["maxRounds"]; !ok {
		t.Fatalf("缺 const 符号: %+v", ix.Syms)
	}
	if s, ok := byName["RunTurn"]; !ok || s.Kind != "method" || s.Recv != "Runner" {
		t.Fatalf("方法解析错误: %+v", s)
	}
	if s, ok := byName["parseConfig"]; !ok || s.Kind != "func" {
		t.Fatalf("函数解析错误: %+v", s)
	}
	if s, ok := byName["Runner"]; !ok || s.Kind != "struct" {
		t.Fatalf("结构体解析错误: %+v", s)
	}
	if s, ok := byName["Doer"]; !ok || s.Kind != "interface" {
		t.Fatalf("接口解析错误: %+v", s)
	}
	if s := byName["RunTurn"]; s.Line <= 0 || s.File != "internal/app/engine.go" {
		t.Fatalf("位置信息错误: %+v", s)
	}
	if _, ok := byName["shouldBeSkipped"]; ok {
		t.Fatal("node_modules 应被跳过")
	}
}

func TestBuildPythonSymbols(t *testing.T) {
	root := sampleRepo(t)
	ix := Build(root, nil)
	got := map[string]string{}
	for _, s := range ix.Syms {
		if s.File == "scripts/tool.py" {
			got[s.Name] = s.Kind
		}
	}
	if got["Fetcher"] != "class" || got["main"] != "def" {
		t.Fatalf("Python 符号解析错误: %+v", got)
	}
}

func TestIncrementalReuse(t *testing.T) {
	root := sampleRepo(t)
	first := Build(root, nil)
	second := Build(root, first)
	if len(first.Syms) != len(second.Syms) {
		t.Fatalf("增量结果条数不一致: %d vs %d", len(first.Syms), len(second.Syms))
	}
	// 改动一个文件后，只有该文件被重解析，符号应随之更新
	writeF(t, root, "scripts/tool.py", `class Fetcher:
    def fetch_one(self, url):
        return url
`)
	third := Build(root, second)
	names := map[string]bool{}
	for _, s := range third.Syms {
		names[s.Name] = true
	}
	if names["fetch_all"] {
		t.Fatal("旧符号未被清除")
	}
	if !names["fetch_one"] {
		t.Fatal("新符号未建立")
	}
	if !names["parseConfig"] {
		t.Fatal("未变化文件的符号丢失")
	}
}

func TestSearchRanking(t *testing.T) {
	root := sampleRepo(t)
	ix := Build(root, nil)

	hits := ix.Search("Runner", 10)
	if len(hits) == 0 || hits[0].Name != "Runner" {
		t.Fatalf("完全匹配应排第一: %+v", hits)
	}

	hits = ix.Search("parse", 10)
	if len(hits) == 0 || hits[0].Name != "parseConfig" {
		t.Fatalf("前缀匹配失败: %+v", hits)
	}

	if got := ix.Search("zzzz", 10); len(got) != 0 {
		t.Fatalf("应无匹配: %+v", got)
	}

	if got := ix.Search("fetch", 1); len(got) != 1 {
		t.Fatalf("limit 未生效: %+v", got)
	}
}

func TestCacheRoundTrip(t *testing.T) {
	root := sampleRepo(t)
	ix := Build(root, nil)
	p := filepath.Join(t.TempDir(), "idx", "codeindex.json")
	if err := ix.SaveTo(p); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	back := LoadFrom(p)
	if back == nil || len(back.Syms) != len(ix.Syms) || back.Root != ix.Root {
		t.Fatalf("缓存读取不一致: %+v", back)
	}
	if bad := LoadFrom(filepath.Join(t.TempDir(), "none.json")); bad != nil {
		t.Fatal("缺失缓存应返回 nil")
	}
}

func TestBoundaryMatch(t *testing.T) {
	cases := []struct {
		name, q string
		want    bool
	}{
		{"parseGoFile", "go", true},
		{"parseGoFile", "parse", true},
		{"parsego", "go", false},
		{"RunTurn", "turn", true},
	}
	for _, c := range cases {
		if got := boundaryMatch(c.name, c.q); got != c.want {
			t.Errorf("boundaryMatch(%q,%q)=%v want %v", c.name, c.q, got, c.want)
		}
	}
}

func TestTruncationFlag(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		writeF(t, root, filepath.ToSlash(filepath.Join("pkg", string(rune('a'+i))+".go")), "package pkg\n\nfunc F"+string(rune('A'+i))+"() {}\n")
	}
	ix := Build(root, nil)
	if ix.Truncated {
		t.Fatal("小仓库不应截断")
	}
	if _, syms := ix.Stats(); syms < 5 {
		t.Fatalf("符号数异常: %d", syms)
	}
}
