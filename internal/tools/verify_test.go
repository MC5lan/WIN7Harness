package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildCommandMatrix(t *testing.T) {
	dir := t.TempDir()
	if got := buildCommand("go", dir); got != "go build ./..." {
		t.Fatalf("go 构建命令 = %q", got)
	}
	if got := buildCommand("cargo", dir); got != "cargo build" {
		t.Fatalf("cargo 构建命令 = %q", got)
	}
	if got := buildCommand("pytest", dir); got != "" {
		t.Fatalf("pytest 不应有构建步骤，得到 %q", got)
	}
	// 没有 package.json 时 npm 不给构建命令
	if got := buildCommand("npm", dir); got != "" {
		t.Fatalf("缺 package.json 时不应有构建命令，得到 %q", got)
	}
	// 有 build 脚本才给
	os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"scripts":{"test":"jest","build":"tsc"}}`), 0o644)
	if got := buildCommand("npm", dir); got != "npm run build" {
		t.Fatalf("有 build 脚本时应返回 npm run build，得到 %q", got)
	}
}

func TestHasNpmScript(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"scripts":{"test":"jest"}}`), 0o644)
	if !hasNpmScript(dir, "test") {
		t.Fatal("应识别到 test 脚本")
	}
	if hasNpmScript(dir, "build") {
		t.Fatal("不应误报 build 脚本")
	}
	if hasNpmScript(t.TempDir(), "test") {
		t.Fatal("缺文件时应返回 false")
	}
}

// 没有可识别框架时必须安全跳过，不能 panic 或误判为通过。
func TestVerifyProjectSkip(t *testing.T) {
	res := VerifyProject(&Context{WorkDir: t.TempDir()}, "", "", 0)
	if res.OK {
		t.Fatal("空目录不应验证通过")
	}
	if res.Skipped == "" {
		t.Fatal("空目录应给出跳过原因")
	}
	if !strings.Contains(res.VerifySummary(), "跳过") {
		t.Fatalf("摘要措辞不符：%s", res.VerifySummary())
	}
}

// 真跑一次 go 构建 + 测试（用临时 go module，验证通过路径）。
func TestVerifyProjectGoPass(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实构建")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module probe\n\ngo 1.20\n",
		"a.go":      "package probe\n\nfunc Add(a, b int) int { return a + b }\n",
		"a_test.go": "package probe\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) { if Add(1,2) != 3 { t.Fatal(\"bad\") } }\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := VerifyProject(&Context{WorkDir: dir}, "", "", 120*time.Second)
	if res.Skipped != "" {
		t.Fatalf("不应跳过：%s", res.Skipped)
	}
	if !res.OK {
		t.Fatalf("应验证通过，报告：%s", res.Report)
	}
}

// 编译错误要在构建阶段就被抓出来，且报告里带出错误行。
func TestVerifyProjectGoBuildFail(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实构建")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module probe\n\ngo 1.20\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package probe\n\nfunc broken() { undefinedSymbol() }\n"), 0o644)
	res := VerifyProject(&Context{WorkDir: dir}, "", "", 120*time.Second)
	if res.OK {
		t.Fatal("编译失败不应判定通过")
	}
	if !strings.Contains(res.Report, "构建失败") {
		t.Fatalf("报告应指出构建失败：%s", res.Report)
	}
	if !strings.Contains(res.Report, "a.go") {
		t.Fatalf("报告应带出出错文件：%s", res.Report)
	}
}
