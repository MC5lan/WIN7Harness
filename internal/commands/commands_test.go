package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanAndFind(t *testing.T) {
	wd := t.TempDir()
	global := GlobalDir()
	// 用临时目录替换全局目录不可行（GlobalDir 读 DataDir），这里只验证项目级扫描。
	dir := ProjectDir(wd)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: review\ndescription: 代码审查\n---\n请审查 {{args}}，重点看错误处理。\n"
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	list := Scan(wd)
	if len(list) != 1 {
		t.Fatalf("want 1 command, got %#v", list)
	}
	c := list[0]
	if c.Name != "review" || c.Desc != "代码审查" || c.Scope != "project" {
		t.Errorf("unexpected command: %#v", c)
	}
	got := c.Expand("main.go")
	if got != "请审查 main.go，重点看错误处理。" {
		t.Errorf("Expand = %q", got)
	}
	if found, ok := Find(wd, "/REVIEW"); !ok || found.Name != "review" {
		t.Error("Find 应大小写不敏感且容忍前导 /")
	}
	// 文件名兜底 name
	os.WriteFile(filepath.Join(dir, "plain.md"), []byte("just body"), 0o644)
	list = Scan(wd)
	if len(list) != 2 || list[0].Name != "plain" || list[1].Name != "review" {
		t.Errorf("文件名兜底/排序失败: %#v", list)
	}
	_ = global
}

func TestProjectDir(t *testing.T) {
	if got := ProjectDir(`D:\work`); got != filepath.Join(`D:\work`, ".harness", "commands") {
		t.Errorf("ProjectDir = %q", got)
	}
}
