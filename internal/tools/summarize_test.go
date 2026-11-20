package tools

import (
	"fmt"
	"strings"
	"testing"
)

func TestSummarizeKeepsShort(t *testing.T) {
	in := "编译通过\n测试通过"
	if got := SummarizeToolOutput(in, 100); got != in {
		t.Fatalf("未超预算应原样返回，得到 %q", got)
	}
}

func TestSummarizeKeepsErrors(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&b, "ok  \twin7harness/internal/pkg%d\t0.001s\n", i)
		if i == 900 {
			b.WriteString("--- FAIL: TestSomething (0.01s)\n    foo/bar.go:123: 期望 1，得到 2\n")
		}
	}
	in := b.String()
	out := SummarizeToolOutput(in, 4000)
	if len(out) >= len(in) {
		t.Fatalf("摘要未压缩：%d -> %d", len(in), len(out))
	}
	if !strings.Contains(out, "foo/bar.go:123") {
		t.Fatal("摘要丢掉了带行号的关键错误行")
	}
	if !strings.Contains(out, "FAIL: TestSomething") {
		t.Fatal("摘要丢掉了失败用例名")
	}
	if !strings.Contains(out, "已省略") {
		t.Fatal("摘要缺少省略标记")
	}
	// 关键行也应该带上第一行与最后一行（头尾保留）
	if !strings.HasPrefix(out, "ok") {
		t.Fatal("丢失头部")
	}
	if !strings.Contains(out, "pkg1999") {
		t.Fatal("丢失尾部")
	}
}

// 重复错误只保留一份，避免摘要被同一条报错刷满。
func TestSummarizeDedupErrors(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "line %d fine\n", i)
	}
	for i := 0; i < 500; i++ {
		b.WriteString("error: cannot find module foo\n")
	}
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "line %d tail\n", i)
	}
	out := SummarizeToolOutput(b.String(), 3000)
	if n := strings.Count(out, "cannot find module foo"); n != 1 {
		t.Fatalf("中间段的重复错误应只留一份，实际 %d 份", n)
	}
}

// 超长单行按字符窗口截断，不能整段丢掉。
func TestSummarizeHugeSingleLine(t *testing.T) {
	in := strings.Repeat("x", 50000)
	out := SummarizeToolOutput(in, 2000)
	if len(out) > 2600 {
		t.Fatalf("超长单行摘要仍过大：%d", len(out))
	}
	if !strings.Contains(out, "已省略") {
		t.Fatal("超长单行缺少省略标记")
	}
}

func TestOutputWasTrimmed(t *testing.T) {
	if OutputWasTrimmed("abc", "abc") {
		t.Fatal("未压缩不应标记")
	}
	if !OutputWasTrimmed(strings.Repeat("a", 100), "abc...（中间 90 字符已省略）") {
		t.Fatal("压缩后应标记")
	}
}
