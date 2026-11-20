package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, dir, sub, body string) {
	t.Helper()
	p := filepath.Join(dir, sub)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanProjectSkill(t *testing.T) {
	// 隔离全局技能目录：Scan 会合并全局技能，本机装过 go-fix-loop 会导致断言红
	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
	wd := t.TempDir()
	writeSkill(t, ProjectDir(wd), "deploy", "---\nname: deploy\ndescription: 发布流程\nwhen: 发版时\n---\n1. build\n2. zip\n")
	list := Scan(wd)
	if len(list) != 1 {
		t.Fatalf("期望 1 个技能，实得 %d", len(list))
	}
	sk := list[0]
	if sk.Name != "deploy" || sk.Desc != "发布流程" || sk.When != "发版时" {
		t.Fatalf("frontmatter 解析异常：%+v", sk)
	}
	if !strings.Contains(sk.Body, "zip") {
		t.Fatalf("正文未解析：%q", sk.Body)
	}
	if sk.Scope != "project" {
		t.Fatalf("作用域应为 project，实得 %s", sk.Scope)
	}
}

func TestFlatMarkdownSkillAndFallbackDesc(t *testing.T) {
	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
	wd := t.TempDir()
	p := filepath.Join(ProjectDir(wd), "quick.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("# 快速修复\n先跑测试。\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list := Scan(wd)
	if len(list) != 1 || list[0].Name != "quick" {
		t.Fatalf("单文件技能未识别：%+v", list)
	}
	if list[0].Desc == "" {
		t.Fatal("缺少 description 时应回落到正文首行")
	}
}

func TestFindCaseInsensitiveAndBlock(t *testing.T) {
	wd := t.TempDir()
	writeSkill(t, ProjectDir(wd), "Fix-Loop", "---\ndescription: 修构建\n---\n步骤\n")
	if _, ok := Find(wd, "fix-loop"); !ok {
		t.Fatal("技能查找应忽略大小写")
	}
	blk := Block(wd)
	if !strings.Contains(blk, "Fix-Loop") || !strings.Contains(blk, "skill_use") {
		t.Fatalf("系统提示注入异常：%q", blk)
	}
}

func TestBlockEmpty(t *testing.T) {
	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
	if Block(t.TempDir()) != "" {
		t.Fatal("没有技能时不应注入任何内容")
	}
}
