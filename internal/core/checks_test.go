package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunChecksOnTempDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	cfg := DefaultConfig()
	cfg.WorkDir = t.TempDir()
	// 指向本地不可达端口：TLS 探测立即失败，测试不依赖外网
	cfg.Providers[cfg.ActiveProvider].BaseURL = "http://127.0.0.1:1/v1"
	cfg.Providers[cfg.ActiveProvider].APIKey = ""
	cfg.Providers[cfg.ActiveProvider].Local = true
	if err := EnsureDirs(cfg); err != nil {
		t.Fatalf("建目录失败: %v", err)
	}
	res := RunChecks(cfg)
	if len(res) < 5 {
		t.Fatalf("检查项过少: %d", len(res))
	}
	var hasDir bool
	for _, r := range res {
		if r.Name == "数据目录可写" {
			hasDir = true
			if r.Status != CheckOK {
				t.Fatalf("临时目录应可写: %+v", r)
			}
		}
		if r.Status == "" || r.Detail == "" {
			t.Fatalf("结果字段不完整: %+v", r)
		}
	}
	if !hasDir {
		t.Fatal("缺少数据目录检查项")
	}
	rep := Report(res)
	if !strings.Contains(rep, "自检结果") || !strings.Contains(rep, "数据目录可写") {
		t.Fatalf("报告内容异常:\n%s", rep)
	}
}

func TestCheckDirWritableFailsOnFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := checkDirWritable("测试", filepath.Join(file, "sub"))
	if r.Status != CheckFail || r.Hint == "" {
		t.Fatalf("文件当目录应失败并给建议: %+v", r)
	}
}

func TestMaskSecret(t *testing.T) {
	cases := map[string]string{
		"":                    "",
		"short":               "****",
		"sk-1234567890abcdef": "sk-1****cdef",
	}
	for in, want := range cases {
		if got := maskSecret(in); got != want {
			t.Fatalf("maskSecret(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestExportDiagContainsFilesAndRedacts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	cfg := DefaultConfig()
	cfg.Providers[cfg.ActiveProvider].BaseURL = "http://127.0.0.1:1/v1"
	cfg.Providers[cfg.ActiveProvider].APIKey = "sk-abcdefghijklmnop"
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "diag.zip")
	p, err := ExportDiag(out, DefaultDiagOptions())
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	if p != out {
		t.Fatalf("路径不符: %s", p)
	}
	names := zipNames(t, p)
	for _, want := range []string{"README.txt", "env.txt", "checks.txt", "config.redacted.json", "logs.txt"} {
		if !names[want] {
			t.Fatalf("缺少 %s，实际: %v", want, names)
		}
	}
	body := zipRead(t, p, "config.redacted.json")
	if strings.Contains(body, "abcdefghijklmnop") {
		t.Fatal("配置中的 API Key 未脱敏")
	}
}

func TestLogTailAndRotate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WIN7HARNESS_HOME", dir)
	InitLog()
	defer CloseLog()
	for i := 0; i < 5; i++ {
		Logf("第 %d 行", i)
	}
	tail := LogTail(3)
	if len(tail) != 3 || !strings.Contains(tail[2], "第 4 行") {
		t.Fatalf("日志尾部异常: %v", tail)
	}
	if !RotateLogIfBig(1) {
		t.Fatal("超过 1 字节应触发轮转")
	}
	if _, err := os.Stat(LogPath() + ".1"); err != nil {
		t.Fatalf("轮转文件不存在: %v", err)
	}
	if LogSize() > 200 {
		t.Fatalf("轮转后新日志应重新开始，实际 %d 字节", LogSize())
	}
}
