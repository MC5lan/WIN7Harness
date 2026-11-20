//go:build windows

package app

import "testing"

// 路径检测要能覆盖消息里常见的三种写法：相对路径、盘符绝对路径、
// 以及带 :行 / :起-止 的行号后缀；同时不能把普通英文或 URL 当路径。
func TestPathTokenDetection(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"请看 internal/ui/gfx.go 的 FillRect", []string{"internal/ui/gfx.go"}},
		{"见 core/diff.go:23 和 docs/a.md:10-20", []string{"core/diff.go:23", "docs/a.md:10-20"}},
		{`打开 D:\proj\main.go:5 看看`, []string{`D:\proj\main.go:5`}},
		{"普通英文单词 nothing here", nil},
		{"访问 https://example.com/a/b.go:12 不是路径", nil},
	}
	for _, tc := range cases {
		got := []string{}
		for _, m := range pathTokenRe.FindAllStringIndex(tc.text, -1) {
			tok := tc.text[m[0]:m[1]]
			if !isURLTail(tc.text, m[0]) && looksLikePathToken(tok) {
				got = append(got, tok)
			}
		}
		if len(got) != len(tc.want) {
			t.Errorf("%q: got %v, want %v", tc.text, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%q: got %v, want %v", tc.text, got, tc.want)
				break
			}
		}
	}
}

func TestLooksLikePathToken(t *testing.T) {
	yes := []string{"foo.go", "internal/ui/gfx.go:12", `C:\p\a.go`, "a/b/c.py:3-7"}
	no := []string{"", "http://x/y.go:12", "https://a.b", "www.example.com", "hello"}
	for _, s := range yes {
		if !looksLikePathToken(s) {
			t.Errorf("应识别为路径: %q", s)
		}
	}
	for _, s := range no {
		if looksLikePathToken(s) {
			t.Errorf("不应识别为路径: %q", s)
		}
	}
}
