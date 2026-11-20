package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseGoVersion(t *testing.T) {
	cases := []struct {
		in       string
		maj, min int
		ok       bool
	}{
		{"1.20", 1, 20, true},
		{"1.21.0", 1, 21, true},
		{"1.17", 1, 17, true},
		{"go1.19", 0, 0, false}, // 正则要求以数字开头
		{"", 0, 0, false},
	}
	for _, c := range cases {
		maj, min, ok := parseGoVersion(c.in)
		if maj != c.maj || min != c.min || ok != c.ok {
			t.Errorf("parseGoVersion(%q) = (%d,%d,%v), want (%d,%d,%v)",
				c.in, maj, min, ok, c.maj, c.min, c.ok)
		}
	}
}

func TestLicenseOf(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LICENSE")
	set := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct{ name, content, want string }{
		{"GPL 正文", "GNU GENERAL PUBLIC LICENSE\nVersion 2, June 1991", "BLOCK:GPL"},
		{"AGPL 正文", "GNU AFFERO GENERAL PUBLIC LICENSE\nVersion 3", "BLOCK:AGPL"},
		{"LGPL 含 GPL 字样应降级", "GNU LESSER GENERAL PUBLIC LICENSE\nVersion 2.1\nGNU General Public License", "LGPL"},
		{"双许可证可选 BSD", "Use is subject to your choice of exactly one of:\n* The FreeType License, similar to BSD\n* GNU General Public License (GPL)", "dual(BSD/GPL)"},
		{"Go 风格 BSD", "Copyright (c) 2009 The Go Authors.\nRedistribution and use in source and binary forms, with or without", "Go-BSD"},
		{"MIT 标准文本", "MIT License\nPermission is hereby granted, free of charge, to any person", "MIT"},
		{"无法识别", "Some custom terms and conditions", "unknown"},
	}
	for _, c := range cases {
		set(c.content)
		got, _ := licenseOf(dir)
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
