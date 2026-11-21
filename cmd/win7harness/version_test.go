//go:build windows

package main

import (
	"runtime"
	"strings"
	"testing"

	"win7harness/internal/core"
)

func TestVersionStringUsesSingleSource(t *testing.T) {
	got := versionString()
	if !strings.Contains(got, "v"+core.Version) {
		t.Fatalf("-version 输出未包含 core.Version：%q", got)
	}
	if !strings.Contains(got, runtime.GOOS+"/"+runtime.GOARCH) {
		t.Fatalf("-version 输出未包含平台信息：%q", got)
	}
	if !strings.Contains(got, "build "+buildTime) {
		t.Fatalf("-version 输出未包含 buildTime：%q", got)
	}
}

func TestCoreVersionIsSemver(t *testing.T) {
	if _, _, _, _, err := core.ParseVersion(core.Version); err != nil {
		t.Fatalf("core.Version 不是合法版本号：%v", err)
	}
}
