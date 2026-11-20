package agent

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestBuildUserMessageNoImages(t *testing.T) {
	m, err := BuildUserMessage("你好", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Parts) != 0 {
		t.Fatal("无图消息不应有 Parts")
	}
	if m.Content != "你好" || m.Role != "user" {
		t.Fatalf("消息内容不符：%+v", m)
	}
}

func TestBuildUserMessageWithImage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.png")
	writePNG(t, p)

	m, err := BuildUserMessage("看图", []string{p})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Parts) != 1 {
		t.Fatalf("应有 1 个图片段，实际 %d", len(m.Parts))
	}
	if !m.HasImages() {
		t.Fatal("HasImages 应为 true")
	}
	if m.Parts[0].ImageURL == nil || !strings.HasPrefix(m.Parts[0].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("图片段不是 data URL：%+v", m.Parts[0])
	}
}

func TestBuildUserMessageMissingFile(t *testing.T) {
	m, err := BuildUserMessage("看图", []string{filepath.Join(t.TempDir(), "nope.png")})
	if err == nil {
		t.Fatal("文件不存在应返回错误提示")
	}
	if len(m.Parts) != 0 {
		t.Fatal("失败时不应有图片段")
	}
	// 文本仍应保留，不阻塞发送
	if m.Content != "看图" {
		t.Fatalf("文本应保留：%q", m.Content)
	}
}

func TestBuildUserMessageCapsCount(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := 0; i < 6; i++ {
		p := filepath.Join(dir, string(rune('a'+i))+".png")
		writePNG(t, p)
		paths = append(paths, p)
	}
	m, err := BuildUserMessage("多图", paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Parts) > 4 {
		t.Fatalf("最多 4 张，实际 %d", len(m.Parts))
	}
}
