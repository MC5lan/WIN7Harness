package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadGoModDirectives(t *testing.T) {
	p := filepath.Join(t.TempDir(), "go.mod")
	body := `module example.com/demo

go 1.21 // 需要新版本

toolchain go1.22.3
`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	goVer, toolchain := readGoModDirectives(p)
	if goVer != "1.21" || toolchain != "1.22.3" {
		t.Fatalf("go=%q toolchain=%q", goVer, toolchain)
	}
}

func TestEvaluateWin7RejectsNewGo(t *testing.T) {
	r := ToolchainReport{
		Win7:        true,
		OS:          "windows",
		GoVersion:   "1.20.14",
		GoMod:       "go.mod",
		GoDirective: "1.22",
	}
	evaluateToolchainPolicy(&r)
	if r.OK() {
		t.Fatal("Win7 + go 1.22 should not be locally buildable")
	}
	if !strings.Contains(strings.Join(r.Problems, " "), "Win7") {
		t.Fatalf("problems = %v", r.Problems)
	}
}

func TestEvaluateWin7Allows120(t *testing.T) {
	for _, directive := range []string{"1.20", "1.20.14"} {
		r := ToolchainReport{
			Win7:        true,
			OS:          "windows",
			GoVersion:   "1.20.14",
			GoMod:       "go.mod",
			GoDirective: directive,
		}
		evaluateToolchainPolicy(&r)
		if !r.OK() {
			t.Fatalf("Win7 + go %s should be buildable: %v", directive, r.Problems)
		}
	}
}

func TestEvaluateWin7GoplsWarning(t *testing.T) {
	r := ToolchainReport{
		Win7:        true,
		GoVersion:   "1.20.14",
		GoMod:       "go.mod",
		GoDirective: "1.20",
		Gopls:       "0.16.2",
	}
	evaluateToolchainPolicy(&r)
	if !r.OK() {
		t.Fatalf("gopls version should not block builds: %v", r.Problems)
	}
	if len(r.Warnings) == 0 || !strings.Contains(strings.Join(r.Warnings, " "), "gopls") {
		t.Fatalf("warnings = %v", r.Warnings)
	}
}

func TestEvaluateWin10UsesNativeToolchain(t *testing.T) {
	r := ToolchainReport{
		Win7:        false,
		GoVersion:   "1.22.5",
		GoMod:       "go.mod",
		GoDirective: "1.22",
	}
	evaluateToolchainPolicy(&r)
	if !r.OK() {
		t.Fatalf("Win10 + go 1.22 should be buildable: %v", r.Problems)
	}
}

func TestEvaluateWin10OldGoCannotMeetNewModule(t *testing.T) {
	r := ToolchainReport{
		Win7:        false,
		GoVersion:   "1.20.14",
		GoMod:       "go.mod",
		GoDirective: "1.22",
	}
	evaluateToolchainPolicy(&r)
	if r.OK() {
		t.Fatal("go 1.20 cannot build a go 1.22 module without toolchain auto-download")
	}
}

func TestEvaluateWin10NewGoWarnsOnFutureModule(t *testing.T) {
	r := ToolchainReport{
		Win7:        false,
		GoVersion:   "1.22.5",
		GoMod:       "go.mod",
		GoDirective: "1.24",
	}
	evaluateToolchainPolicy(&r)
	if !r.OK() {
		t.Fatalf("Go 1.22 may auto-download 1.24: %v", r.Problems)
	}
	if len(r.Warnings) == 0 || !strings.Contains(strings.Join(r.Warnings, " "), "GOTOOLCHAIN") {
		t.Fatalf("warnings = %v", r.Warnings)
	}
}

func TestHighestGoDirective(t *testing.T) {
	if got := highestGoDirective("1.20", "go1.22.3", "1.21"); got != "1.22.3" {
		t.Fatalf("got %q", got)
	}
	if got := highestGoDirective("", "", ""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestCmpVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.20.14", "1.20", 1},
		{"1.20", "1.20.0", 0},
		{"1.19.13", "1.20.14", -1},
		{"v0.15.3", "0.15.3", 0},
		{"0.16.0", "0.15.3", 1},
	}
	for _, c := range cases {
		if got := cmpVersion(c.a, c.b); got != c.want {
			t.Errorf("cmpVersion(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckGoToolchainHonorsW7HGO(t *testing.T) {
	if os.Getenv("W7H_GO") == "" {
		t.Skip("W7H_GO not set")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/demo\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := CheckGoToolchain(dir)
	if r.GoVersion == "" {
		t.Fatalf("W7H_GO 未被用于探测：%+v", r)
	}
	if !r.OK() {
		t.Fatalf("go 1.20 项目应可构建：%v", r.Problems)
	}
}

func TestOSIsWin7(t *testing.T) {
	if !osIsWin7("windows", "Windows 6.1.7601") {
		t.Fatal("6.1 should be Win7")
	}
	if osIsWin7("windows", "Windows 10.0.22631") {
		t.Fatal("10.0 should not be Win7")
	}
	if osIsWin7("linux", "Windows 6.1.7601") {
		t.Fatal("non-windows should not be Win7")
	}
}
