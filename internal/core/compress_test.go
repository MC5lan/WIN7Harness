package core

import (
	"bytes"
	"strings"
	"testing"
)

func TestGzipIfSmallerThreshold(t *testing.T) {
	small := []byte("hello")
	if got := GzipIfSmaller(small, 96); got != nil {
		t.Fatalf("低于阈值不应压缩，得到 %d 字节", len(got))
	}
	if got := GzipIfSmaller(small, 0); got != nil {
		// "hello" 压完反而更大，应当明确放弃压缩
		t.Fatalf("压缩无收益时应返回 nil，得到 %d 字节", len(got))
	}
	if got := GzipIfSmaller(bytes.Repeat([]byte("ab"), 512), 0); got == nil {
		t.Fatal("有价值的重复数据应被压缩")
	}
}

func TestGzipRoundTrip(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 4000; i++ {
		b.WriteString(`{"role":"assistant","text":"这是一段重复度很高的正文内容"}` + "\n")
	}
	raw := []byte(b.String())
	gz := GzipIfSmaller(raw, 1024)
	if gz == nil {
		t.Fatal("高重复度数据应被压缩")
	}
	if len(gz) >= len(raw)/2 {
		t.Fatalf("压缩比不理想: %d -> %d", len(raw), len(gz))
	}
	if !IsGzipData(gz) {
		t.Fatal("魔数识别失败")
	}
	out, err := GunzipData(gz, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, raw) {
		t.Fatal("解压内容与原文不一致")
	}
}

func TestGunzipPassThroughAndLimit(t *testing.T) {
	raw := []byte("plain text")
	out, err := GunzipData(raw, 0)
	if err != nil || !bytes.Equal(out, raw) {
		t.Fatalf("非 gzip 应原样返回: %v %q", err, out)
	}

	big := bytes.Repeat([]byte("x"), 4096)
	gz := GzipIfSmaller(big, 0)
	if gz == nil {
		t.Fatal("应压缩")
	}
	if _, err := GunzipData(gz, 1024); err == nil {
		t.Fatal("超过解压上限应报错")
	}
}
