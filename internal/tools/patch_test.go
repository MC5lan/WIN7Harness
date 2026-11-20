package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyHunksBasic(t *testing.T) {
	src := "a\nb\nc\nd\n"
	patch := "*** Begin Patch\n*** Update File: x.go\n@@\n a\n-b\n+B\n c\n*** End Patch\n"
	ps, err := parsePatch(patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].path != "x.go" || len(ps[0].hunks) != 1 {
		t.Fatalf("解析结果异常: %+v", ps)
	}
	out, err := applyHunks(src, ps[0].hunks)
	if err != nil {
		t.Fatal(err)
	}
	if out != "a\nB\nc\nd\n" {
		t.Fatalf("got %q", out)
	}
}

func TestApplyHunksMultiAndWhitespaceTolerance(t *testing.T) {
	src := "func a() {\n\tx := 1\n}\n\nfunc b() {\n\ty := 2\n}\n"
	// 第二段故意用 4 空格缩进，应被容错匹配到 tab 缩进
	patch := "*** Begin Patch\n*** Update File: p.go\n@@ a\n \tx := 1\n+\tx++\n@@ b\n-    y := 2\n+    y := 3\n*** End Patch\n"
	ps, err := parsePatch(patch)
	if err != nil {
		t.Fatal(err)
	}
	out, err := applyHunks(src, ps[0].hunks)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "x :="+" 1\n\tx++") {
		t.Fatalf("第一段未生效: %q", out)
	}
	if !strings.Contains(out, "y := 3") || strings.Contains(out, "y := 2") {
		t.Fatalf("第二段未生效: %q", out)
	}
}

func TestApplyHunksNoMatch(t *testing.T) {
	_, err := applyHunks("a\nb\n", []patchHunk{{lines: []string{"-zzz", "+yyy"}}})
	if err == nil || !strings.Contains(err.Error(), "未能匹配") {
		t.Fatalf("应报定位失败，实际: %v", err)
	}
}

func TestApplyHunksPreserveCRLF(t *testing.T) {
	src := "a\r\nb\r\n"
	ps, _ := parsePatch("*** Begin Patch\n*** Update File: c.txt\n@@\n-b\n+c\n*** End Patch\n")
	out, err := applyHunks(src, ps[0].hunks)
	if err != nil {
		t.Fatal(err)
	}
	if out != "a\r\nc\r\n" {
		t.Fatalf("换行风格丢失: %q", out)
	}
}

func TestParsePatchRejectsUnknownLine(t *testing.T) {
	if _, err := parsePatch("*** Begin Patch\n*** Update File: a.go\n随便一行\n*** End Patch"); err == nil {
		t.Fatal("应以 /-/ /+ 开头，普通行应报错")
	}
	if _, err := parsePatch("*** Begin Patch\n*** Delete File: a.go\n*** End Patch"); err == nil {
		t.Fatal("删除文件应被拒绝")
	}
}

func TestRunApplyPatchDryRunAndApply(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main.go")
	if err := os.WriteFile(p, []byte("package main\n\nfunc main() {\n\tprintln(1)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := &Context{WorkDir: dir}
	patch := "*** Begin Patch\n*** Update File: main.go\n@@\n-\tprintln(1)\n+\tprintln(2)\n*** Add File: util.go\n+package main\n+\n+func helper() int { return 42 }\n*** End Patch\n"
	args := map[string]interface{}{"patch": patch, "dry_run": true}
	if _, err := runApplyPatch(ctx, args); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); !strings.Contains(string(got), "println(1)") {
		t.Fatal("dry_run 不应写盘")
	}
	if _, err := os.Stat(filepath.Join(dir, "util.go")); err == nil {
		t.Fatal("dry_run 不应创建文件")
	}

	args["dry_run"] = false
	res, err := runApplyPatch(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "2 个文件") {
		t.Fatalf("结果说明异常: %s", res.Output)
	}
	got, _ := os.ReadFile(p)
	if !strings.Contains(string(got), "println(2)") {
		t.Fatalf("未写入: %s", got)
	}
	u, err := os.ReadFile(filepath.Join(dir, "util.go"))
	if err != nil || !strings.Contains(string(u), "return 42") {
		t.Fatalf("新文件异常: %v %s", err, u)
	}
}

func TestRunApplyPatchPreservesGBKCRLF(t *testing.T) {
	t.Setenv("WIN7HARNESS_PORTABLE", "1")
	dir := t.TempDir()
	p := filepath.Join(dir, "a.c")
	raw, err := encodeText("第一行\r\n第二行\r\n", textMeta{Enc: textGB18030, EOL: "\r\n"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Update File: a.c\n@@\n-第二行\n+第三行\n*** End Patch\n"
	if _, err := runApplyPatch(&Context{WorkDir: dir}, map[string]interface{}{"patch": patch}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text, meta, err := decodeTextBytes(got)
	if err != nil {
		t.Fatal(err)
	}
	if text != "第一行\r\n第三行\r\n" {
		t.Fatalf("text = %q", text)
	}
	if meta.Enc != textGB18030 || meta.EOL != "\r\n" {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestRunApplyPatchRejectsExistingAdd(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	_, err := runApplyPatch(&Context{WorkDir: dir}, map[string]interface{}{
		"patch": "*** Begin Patch\n*** Add File: a.txt\n+x\n*** End Patch",
	})
	if err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("应拒绝覆盖已存在文件，实际: %v", err)
	}
}
