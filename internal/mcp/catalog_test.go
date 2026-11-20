package mcp

import (
	"strings"
	"testing"

	"win7harness/internal/core"
)

// 所有内置模板必须自洽，否则用户点了安装才报错，体验很差。
func TestCatalogTemplatesValid(t *testing.T) {
	seen := map[string]bool{}
	for _, tpl := range Catalog() {
		if err := tpl.Validate(); err != nil {
			t.Fatalf("模板 %s 不合法：%v", tpl.ID, err)
		}
		if seen[tpl.ID] {
			t.Fatalf("模板 ID 重复：%s", tpl.ID)
		}
		seen[tpl.ID] = true
	}
	if len(Catalog()) < 5 {
		t.Fatalf("模板太少：%d", len(Catalog()))
	}
}

// 序号与 ID 两种选法都要能用，越界要给 false。
func TestFindTemplate(t *testing.T) {
	if tpl, ok := FindTemplate("1"); !ok || tpl.ID != Catalog()[0].ID {
		t.Fatal("序号 1 应命中第一个模板")
	}
	if tpl, ok := FindTemplate("GITHUB"); !ok || tpl.ID != "github" {
		t.Fatal("ID 应大小写不敏感")
	}
	if _, ok := FindTemplate("0"); ok {
		t.Fatal("序号 0 无效")
	}
	if _, ok := FindTemplate("99"); ok {
		t.Fatal("越界序号无效")
	}
	if _, ok := FindTemplate("不存在"); ok {
		t.Fatal("未知 ID 无效")
	}
}

// 目录占位要被真实路径替换，且不能破坏参数顺序。
func TestBuildReplacesDirPlaceholder(t *testing.T) {
	tpl, _ := FindTemplate("filesystem")
	spec, err := tpl.Build(map[string]string{"dir": `C:\my work`})
	if err != nil {
		t.Fatal(err)
	}
	if !spec.Enabled {
		t.Fatal("装好即启用")
	}
	if spec.Name != "filesystem" {
		t.Fatalf("服务名 = %s", spec.Name)
	}
	if spec.Args[len(spec.Args)-1] != `C:\my work` {
		t.Fatalf("目录未正确替换：%v", spec.Args)
	}
	if strings.Contains(strings.Join(spec.Args, " "), "{dir}") {
		t.Fatal("不应残留占位符")
	}
}

// 必填参数缺失要给出可执行的提示，而不是静默生成坏配置。
func TestBuildMissingRequired(t *testing.T) {
	tpl, _ := FindTemplate("git")
	_, err := tpl.Build(map[string]string{})
	if err == nil {
		t.Fatal("缺必填参数应报错")
	}
	if !strings.Contains(err.Error(), "仓库路径") {
		t.Fatalf("错误信息应指明缺什么：%v", err)
	}
}

// Token 属于敏感值：必须进环境变量，不能出现在命令行参数里。
func TestBuildTokenGoesToEnv(t *testing.T) {
	tpl, _ := FindTemplate("github")
	spec, err := tpl.Build(map[string]string{"token": ` "ghp_abc123" `})
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Env) != 1 || spec.Env[0] != "GITHUB_PERSONAL_ACCESS_TOKEN=ghp_abc123" {
		t.Fatalf("token 应规整后写入 env：%v", spec.Env)
	}
	for _, a := range spec.Args {
		if strings.Contains(a, "ghp_abc123") {
			t.Fatal("token 不应出现在 args 中")
		}
	}
}

// 参数里带换行是明显的注入/粘贴错误，要拒绝。
func TestBuildRejectsNewline(t *testing.T) {
	tpl, _ := FindTemplate("filesystem")
	if _, err := tpl.Build(map[string]string{"dir": "C:\\a\nrm -rf /"}); err == nil {
		t.Fatal("含换行的参数应被拒绝")
	}
}

// 无参数模板可以直接装。
func TestBuildNoParams(t *testing.T) {
	tpl, _ := FindTemplate("memory")
	spec, err := tpl.Build(nil)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Command != "npx" || len(spec.Args) != 2 {
		t.Fatalf("参数不对：%v %v", spec.Command, spec.Args)
	}
}

// 已装判断按名字忽略大小写，避免重复安装同款。
func TestInstalled(t *testing.T) {
	specs := []core.MCPServer{{Name: "GitHub"}, {Name: "fs"}}
	if !Installed(specs, "github") {
		t.Fatal("应识别为已安装")
	}
	if Installed(specs, "git") {
		t.Fatal("未安装的不应误判")
	}
	if got := Names(specs); len(got) != 2 || got[0] != "GitHub" {
		t.Fatalf("Names 应排序：%v", got)
	}
}
