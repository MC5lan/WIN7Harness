//go:build windows

package app

import (
	"testing"

	"win7harness/internal/ui"
)

func TestHitOrderTopBarButtonsWin(t *testing.T) {
	// set.top 是顶栏兜底点击区，必须先注册；保存/返回按钮后注册才能拿到点击。
	u := &UI{hits: []hit{
		{id: "set.top", rect: ui.R(0, 0, 900, 56)},
		{id: "set.save.top", rect: ui.R(700, 14, 84, 30)},
	}}
	if got := u.HitAt(710, 20); got != "set.save.top" {
		t.Fatalf("顶栏按钮被兜底区吞掉：%q", got)
	}
	if got := u.HitAt(400, 20); got != "set.top" {
		t.Fatalf("兜底区未生效：%q", got)
	}
}

func TestParseWindowSize(t *testing.T) {
	cases := map[string]int{
		"auto": 0, "0": 0, "32k": 32000, "128K": 128000,
		"200k": 200000, "1m": 1000000, "64000": 64000,
	}
	for in, want := range cases {
		got, ok := parseWindowSize(in)
		if !ok || got != want {
			t.Fatalf("parseWindowSize(%q) = %d,%v，期望 %d", in, got, ok, want)
		}
	}
	if _, ok := parseWindowSize("abc"); ok {
		t.Fatal("非法值应失败")
	}
}
