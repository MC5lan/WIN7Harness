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

func isolateBuiltins(t *testing.T) {
	t.Helper()
	t.Setenv("WIN7H_DISABLE_BUILTIN_SKILLS", "1")
}

func TestScanProjectSkill(t *testing.T) {
	isolateBuiltins(t)
	// 隔离全局技能目录：Scan 会合并全局技能，本机装过 go-fix-loop 会导致断言红。
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
	isolateBuiltins(t)
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
	isolateBuiltins(t)
	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
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
	isolateBuiltins(t)
	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
	if Block(t.TempDir()) != "" {
		t.Fatal("没有技能时不应注入任何内容")
	}
}

func TestBuiltinSkillsEmbedded(t *testing.T) {
	t.Setenv("WIN7H_DISABLE_BUILTIN_SKILLS", "")
	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
	list := BuiltinSkills()
	if len(list) < 8 {
		t.Fatalf("内置技能数量不足：%d", len(list))
	}
	want := map[string]bool{
		"repo-onboarding": false, "go-fix-loop": false, "bug-repro": false,
		"safe-refactor": false, "review-diff": false, "win7-compat": false,
		"release-check": false, "incident-triage": false, "context-recovery": false,
	}
	for _, sk := range list {
		if _, ok := want[sk.Name]; ok {
			want[sk.Name] = true
		}
		if sk.Scope != "builtin" || !sk.ReadOnly || sk.Source != "embedded" {
			t.Fatalf("内置技能元数据异常：%+v", sk)
		}
		if strings.TrimSpace(sk.Desc) == "" || strings.TrimSpace(sk.When) == "" || strings.TrimSpace(sk.Body) == "" {
			t.Fatalf("内置技能 %s 缺少描述/触发条件/正文", sk.Name)
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("缺少内置技能 %s", name)
		}
	}
	if blk := Block(t.TempDir()); len([]rune(blk)) > 4096 {
		t.Fatalf("技能索引过长：%d 字符", len([]rune(blk)))
	}
}

func TestBuiltinPrecedenceAndMetaOnly(t *testing.T) {
	t.Setenv("WIN7H_DISABLE_BUILTIN_SKILLS", "")
	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
	wd := t.TempDir()
	// 项目同名技能应覆盖内置 go-fix-loop。
	writeSkill(t, ProjectDir(wd), "go-fix-loop", "---\nname: go-fix-loop\ndescription: 我的修复流程\nwhen: 本地\n---\n只做本地步骤\n")
	list := ScanMeta(wd)
	var got *Skill
	for i := range list {
		if list[i].Name == "go-fix-loop" {
			got = &list[i]
			break
		}
	}
	if got == nil || got.Scope != "project" || got.Desc != "我的修复流程" {
		t.Fatalf("项目技能未覆盖内置：%+v", got)
	}
	if got.Body != "" {
		t.Fatalf("ScanMeta 不应读取技能正文：%q", got.Body)
	}
	full, ok := Find(wd, "go-fix-loop")
	if !ok || !strings.Contains(full.Body, "只做本地步骤") {
		t.Fatalf("Find 未加载正文：%+v", full)
	}
}

func TestSuggestProjectSkills(t *testing.T) {
	isolateBuiltins(t)
	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
	wd := t.TempDir()
	writeSkill(t, ProjectDir(wd), "go-release", "---\nname: go-release\ndescription: Go 项目发布与打包\nwhen: 发版时\n---\n步骤\n")
	writeSkill(t, ProjectDir(wd), "ui-regression", "---\nname: ui-regression\ndescription: 界面回归\nwhen: 改 UI 时\n---\n步骤\n")
	got := Suggest(wd, "帮我发版并打包 Go 程序", 3)
	if len(got) == 0 || got[0].Name != "go-release" {
		t.Fatalf("技能推荐异常：%+v", got)
	}
	if len(Suggest(wd, "完全无关的天气问题", 3)) != 0 {
		t.Fatal("无关查询不应推荐技能")
	}
}

func TestBuiltinCanBeDisabled(t *testing.T) {
	t.Setenv("WIN7H_DISABLE_BUILTIN_SKILLS", "")
	t.Setenv("WIN7H_DISABLED_SKILLS", "release-check")
	t.Setenv("WIN7HARNESS_HOME", t.TempDir())
	wd := t.TempDir()
	for _, sk := range ScanMeta(wd) {
		if sk.Name == "release-check" {
			t.Fatal("禁用列表未生效")
		}
	}
}
