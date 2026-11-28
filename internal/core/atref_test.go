package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParsePathLineRef(t *testing.T) {
	cases := []struct {
		in       string
		path     string
		from, to int
	}{
		{"main.go", "main.go", 0, 0},
		{"main.go:42", "main.go", 42, 0},
		{"internal/ui/gfx.go:120-180", "internal/ui/gfx.go", 120, 180},
		{`D:\proj\main.go:5`, `D:\proj\main.go`, 5, 0},
		{"C:/proj/a.go:1-2", "C:/proj/a.go", 1, 2},
		{"main.go:abc", "main.go:abc", 0, 0},   // 非法后缀原样保留
		{"main.go:0", "main.go:0", 0, 0},       // 行号 0 视为无行号
		{"http://x/y:12", "http://x/y", 12, 0}, // 解析器本身不管 URL，调用方过滤
	}
	for _, tc := range cases {
		p, from, to := ParsePathLineRef(tc.in)
		if p != tc.path || from != tc.from || to != tc.to {
			t.Errorf("ParsePathLineRef(%q) = (%q, %d, %d), want (%q, %d, %d)",
				tc.in, p, from, to, tc.path, tc.from, tc.to)
		}
	}
}

func TestExpandAtRefs(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	os.WriteFile(filepath.Join(root, "docs", "a b.md"), []byte("hello"), 0o644)

	got := ExpandAtRefs("看看 @main.go 的入口，还有 @docs/a b.md", root)
	if !strings.Contains(got, `<file path="main.go">`) || !strings.Contains(got, "package main") {
		t.Fatalf("@ 引用未展开: %q", got)
	}

	// 越界与不存在文件不展开
	bad := ExpandAtRefs("读取 @../secret.txt 和 @nope.txt", root)
	if strings.Contains(bad, "<file") {
		t.Fatalf("不应展开越界/不存在路径: %q", bad)
	}

	// 绝对路径不能绕过 read_file 的工作区边界。
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP-SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = ExpandAtRefs("读取 @"+secret, root)
	if strings.Contains(got, "TOP-SECRET") || strings.Contains(got, "<file") {
		t.Fatalf("绝对路径不应被 @ 引用展开: %q", got)
	}

	// UNC 在 Windows 上会在 Stat 前访问网络；必须在解析前拒绝。
	got = ExpandAtRefs(`读取 @\\server\share\secret.txt`, root)
	if strings.Contains(got, "<file") {
		t.Fatalf("UNC 路径不应被 @ 引用展开: %q", got)
	}

	// 无 @ 时原样返回
	if ExpandAtRefs("普通问题", root) != "普通问题" {
		t.Fatal("无 @ 时应原样返回")
	}
}

func TestGitRunLocal(t *testing.T) {
	if !GitAvailable() {
		t.Skip("本机无 git")
	}
	dir := t.TempDir()
	if _, err := GitRun(dir, "init"); err != nil {
		t.Skip("git init 失败，跳过")
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	out, err := GitRun(dir, "status", "--short")
	if err != nil {
		t.Fatalf("git status 失败: %v", err)
	}
	if !strings.Contains(out, "a.txt") {
		t.Fatalf("状态输出异常: %q", out)
	}
}

func TestExpandAtRefsWithLine(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 200; i++ {
		b.WriteString("line")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("\n")
	}
	os.WriteFile(filepath.Join(root, "big.go"), []byte(b.String()), 0o644)

	// 单行引用：应带上下文窗口，且只截取一段而非全文
	got := ExpandAtRefs("看 @big.go:100", root)
	if !strings.Contains(got, `<file path="big.go:60-140">`) {
		t.Fatalf("行号引用未按窗口切片: %s", head(got, 200))
	}
	if !strings.Contains(got, "line100") || !strings.Contains(got, "line60") || !strings.Contains(got, "line140") {
		t.Fatalf("窗口内容缺失: %s", head(got, 200))
	}
	if strings.Contains(got, "line1\n") || strings.Contains(got, "line200") {
		t.Fatalf("窗口外的行不应出现: %s", head(got, 300))
	}

	// 区间引用
	got = ExpandAtRefs("看 @big.go:10-20", root)
	if !strings.Contains(got, `<file path="big.go:1-60">`) {
		t.Fatalf("区间引用切片错误: %s", head(got, 200))
	}

	// 行号越界不应崩
	got = ExpandAtRefs("看 @big.go:9999", root)
	if !strings.Contains(got, "<file") {
		t.Fatalf("越界行号应退化为文件末尾片段: %s", head(got, 200))
	}
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
