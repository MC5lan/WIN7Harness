package tools

import (
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestHTMLToText(t *testing.T) {
	html := `<!DOCTYPE html><html><head><title>Go 1.22 发布说明</title>
<style>body{color:#fff}</style><script>var a=1<2;</script></head>
<body><h1>Release Notes</h1><p>修复了 <b>embed</b> 包的 &quot;路径&quot; 问题&amp;其他。</p>
<ul><li>第一项</li><li>第二项&nbsp;含空格</li></ul>
<table><tr><td>a</td><td>b</td></tr></table></body></html>`

	title := pageTitle(html)
	if title != "Go 1.22 发布说明" {
		t.Fatalf("标题解析错误: %q", title)
	}

	text := htmlToText(html)
	for _, want := range []string{"Release Notes", "修复了 embed 包的 \"路径\" 问题&其他。", "- 第一项", "- 第二项 含空格", "a | b"} {
		if !strings.Contains(text, want) {
			t.Fatalf("缺少片段 %q\n实际:\n%s", want, text)
		}
	}
	for _, bad := range []string{"var a=1", "color:#fff", "<p>", "<li>"} {
		if strings.Contains(text, bad) {
			t.Fatalf("应剔除 %q\n实际:\n%s", bad, text)
		}
	}
	if strings.Contains(text, "\n\n\n") {
		t.Fatalf("空行未压缩:\n%s", text)
	}
}

func TestDecodeEntities(t *testing.T) {
	cases := map[string]string{
		"a&amp;b":         "a&b",
		"&lt;div&gt;":     "<div>",
		"&#65;&#x42;":     "AB",
		"&nbsp;x&mdash;y": " x—y",
		"&unknown;z":      "&unknown;z",
		"&#999999999;":    "&#999999999;",
	}
	for in, want := range cases {
		if got := decodeEntities(in); got != want {
			t.Fatalf("decodeEntities(%q)=%q，期望 %q", in, got, want)
		}
	}
}

func TestCheckPublicURLBlocksInternal(t *testing.T) {
	blocked := []string{
		"http://localhost:8080/x",
		"http://127.0.0.1/",
		"http://10.1.2.3/a",
		"http://192.168.1.1/",
		"http://172.16.5.5/",
		"http://169.254.169.254/latest/meta-data/",
		"http://100.64.0.1/",
		"http://[::1]/",
		"http://0.0.0.0/",
		"http://printer.local/",
		"file:///C:/Windows/win.ini",
		"ftp://example.com/a",
	}
	for _, raw := range blocked {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s 解析失败: %v", raw, err)
		}
		if err := checkPublicURL(u); err == nil {
			t.Fatalf("%s 应被拒绝", raw)
		}
	}

	u, _ := url.Parse("https://8.8.8.8/")
	if err := checkPublicURL(u); err != nil {
		t.Fatalf("公网 IP 不应被拒绝: %v", err)
	}
}

func TestIsPublicIP(t *testing.T) {
	yes := []string{"8.8.8.8", "1.1.1.1", "223.5.5.5", "2001:4860:4860::8888"}
	no := []string{"127.0.0.1", "10.0.0.1", "192.168.0.1", "172.20.1.1", "169.254.1.1", "::1", "fe80::1", "224.0.0.1", "0.0.0.0"}
	for _, s := range yes {
		if !isPublicIP(net.ParseIP(s)) {
			t.Fatalf("%s 应为公网", s)
		}
	}
	for _, s := range no {
		if isPublicIP(net.ParseIP(s)) {
			t.Fatalf("%s 应为内网/保留地址", s)
		}
	}
}

func TestCharsetAndTextual(t *testing.T) {
	if got := charsetOf("text/html; charset=GBK", nil); got != "gbk" {
		t.Fatalf("charset 解析错误: %q", got)
	}
	if got := charsetOf("text/html", []byte(`<meta http-equiv="Content-Type" content="text/html; charset=utf-8">`)); got != "utf-8" {
		t.Fatalf("meta charset 解析错误: %q", got)
	}
	if isUTF8Compatible("gbk") {
		t.Fatal("gbk 不应判为 utf-8 兼容")
	}
	if !isUTF8Compatible("") || !isUTF8Compatible("UTF-8") {
		t.Fatal("utf-8 兼容判断错误")
	}
	if isTextual("image/png") || !isTextual("application/json") || !isTextual("") {
		t.Fatal("文本类型判断错误")
	}
}

func TestReadLimited(t *testing.T) {
	b, cut, err := readLimited(strings.NewReader(strings.Repeat("x", 100)), 10)
	if err != nil || len(b) != 10 || !cut {
		t.Fatalf("截断逻辑异常: len=%d cut=%v err=%v", len(b), cut, err)
	}
	b2, cut2, _ := readLimited(strings.NewReader("abc"), 10)
	if string(b2) != "abc" || cut2 {
		t.Fatal("未超限不应标记截断")
	}
}
