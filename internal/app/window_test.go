//go:build windows

package app

import "testing"

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
