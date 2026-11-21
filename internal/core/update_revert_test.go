package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPrepareRevertFromMissingOld(t *testing.T) {
	dir := t.TempDir()
	fakeExe := filepath.Join(dir, "Win7Harness.exe")
	if err := os.WriteFile(fakeExe, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := PrepareRevertFrom(fakeExe)
	if err == nil {
		t.Fatal("缺少 .old 时必须报错")
	}
	if !strings.Contains(err.Error(), "没有可回退的版本") {
		t.Fatalf("错误信息应说明原因，实际：%v", err)
	}
}

func TestPrepareRevertFromValidOld(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("需要 Windows PE 作为夹具：os.Executable() 在非 Windows 上是 ELF")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("无法确定测试二进制路径：%v", err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 1<<20 {
		data = append(data, make([]byte, (1<<20)-len(data))...)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "harness.exe")
	if err := os.WriteFile(target, []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target+".old", data, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := PrepareRevertFrom(target)
	if err != nil {
		t.Fatalf("合法 PE 的 .old 应可回退：%v", err)
	}
	if info.OldArch != "386" && info.OldArch != "amd64" {
		t.Fatalf("架构识别错误：%q", info.OldArch)
	}
	if info.OldSize < 1<<20 {
		t.Fatalf("读回的 .old 大小不对：%d", info.OldSize)
	}
	if info.OldPath != target+".old" {
		t.Fatalf(".old 路径不对：%q", info.OldPath)
	}
}

func TestRevertScriptStructure(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Win7 Harness 测试.exe") // 含空格与中文
	s := RevertScript(exe, true)
	want := []string{
		`set "TARGET=` + batValue(exe) + `"`,
		`set "OLD=%TARGET%.old"`,
		`set "NEW=%TARGET%.new"`,
		`move /y "%TARGET%" "%NEW%"`,
		`move /y "%OLD%" "%TARGET%"`,
		`goto rollback`,
		`:rollback`,
		`:giveup`,
		`start "" "%TARGET%"`,
		`exit /b 0`,
		`exit /b 1`,
	}
	for _, w := range want {
		if !strings.Contains(s, w) {
			t.Fatalf("回退脚本缺少 %q:\n%s", w, s)
		}
	}
	// 空格/中文路径：含 %TARGET% 的行必须含引号（不允许裸引用）
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "%TARGET%") && !strings.Contains(line, `"`) {
			t.Fatalf("含空格路径下未加引号的 %%TARGET%% 引用：%q", line)
		}
	}
}

func TestRevertScriptNoInjection(t *testing.T) {
	// 恶意路径包含引号与 % 时，batValue 必须先转义，不能注入
	evil := `c:\evil" && calc.exe && " & %TEMP%`
	s := RevertScript(evil, true)
	if strings.Contains(s, `" && calc.exe`) {
		t.Fatalf("路径中的命令注入未被过滤：\n%s", s)
	}
	if !strings.Contains(s, "%%TEMP%%") {
		t.Fatalf("百分比未被双写：\n%s", s)
	}
}
