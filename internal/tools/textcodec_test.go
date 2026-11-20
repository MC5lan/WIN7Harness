package tools

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func TestDecodeGB18030CRLF(t *testing.T) {
	raw, _, err := transform.String(simplifiedchinese.GB18030.NewEncoder(), "第一行\r\n第二行\r\n")
	if err != nil {
		t.Fatal(err)
	}
	text, meta, err := decodeTextBytes([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if text != "第一行\r\n第二行\r\n" {
		t.Fatalf("text = %q", text)
	}
	if meta.Enc != textGB18030 {
		t.Fatalf("enc = %s", meta.Enc)
	}
	if meta.EOL != "\r\n" || meta.BOM {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestDecodeUTF16LEBOM(t *testing.T) {
	raw, err := encodeText("中文\r\nabc\r\n", textMeta{Enc: textUTF16LE, BOM: true, EOL: "\r\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) {
		t.Fatalf("missing UTF-16LE BOM: %x", raw[:4])
	}
	text, meta, err := decodeTextBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if text != "中文\r\nabc\r\n" {
		t.Fatalf("text = %q", text)
	}
	if meta.Enc != textUTF16LE || !meta.BOM || meta.EOL != "\r\n" {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestDecodeUTF8BOM(t *testing.T) {
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte("中文\n")...)
	text, meta, err := decodeTextBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if text != "中文\n" {
		t.Fatalf("text = %q", text)
	}
	if meta.Enc != textUTF8 || !meta.BOM || meta.EOL != "\n" {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestDecodeCP1252(t *testing.T) {
	// GB18030 解码 0xE9 单独出现会失败，随后应回退到 CP1252。
	text, meta, err := decodeTextBytes([]byte("caf\xe9\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if text != "café\r\n" {
		t.Fatalf("text = %q", text)
	}
	if meta.Enc != textCP1252 {
		t.Fatalf("enc = %s", meta.Enc)
	}
}

func TestDecodeBinaryRejected(t *testing.T) {
	for _, raw := range [][]byte{
		{0x00, 0x01, 0x02, 0x03},
		{0x89, 'P', 'N', 'G', 0x00, 0x00, 0x01},
		[]byte("%PDF-1.7 no nul here"),
		[]byte("MZ\x90\x00fake PE"),
	} {
		if _, _, err := decodeTextBytes(raw); !errors.Is(err, errBinaryText) {
			t.Fatalf("raw %x: err = %v, want binary", raw, err)
		}
	}
}

func TestEncodePreservesEOLAndBOM(t *testing.T) {
	meta := textMeta{Enc: textGB18030, EOL: "\r\n"}
	raw, err := encodeText("第一行\n第二行\n", meta)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("\n")) && !bytes.Contains(raw, []byte("\r\n")) {
		t.Fatalf("CRLF not applied: %x", raw)
	}
	text, got, err := decodeTextBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if text != "第一行\r\n第二行\r\n" || got.Enc != textGB18030 || got.EOL != "\r\n" {
		t.Fatalf("round trip text=%q meta=%+v", text, got)
	}
}

func TestEncodeCP1252RejectsChinese(t *testing.T) {
	// GB18030 本身覆盖全部 Unicode（含 emoji），但 CP1252 不行。
	// 老英文文件里塞中文必须明确报错，不能静默写成 '?'。
	_, err := encodeText("中文\n", textMeta{Enc: textCP1252, EOL: "\r\n"})
	if err == nil {
		t.Fatal("Chinese should not be encodable as Windows-1252")
	}
}

func TestDefaultTextMetaForScripts(t *testing.T) {
	bat := defaultTextMeta(`C:\tmp\run.bat`)
	if bat.Enc != textGB18030 || bat.BOM || bat.EOL != "\r\n" {
		t.Fatalf("bat meta = %+v", bat)
	}
	ps1 := defaultTextMeta(`C:\tmp\run.ps1`)
	if ps1.Enc != textUTF8 || !ps1.BOM || ps1.EOL != "\r\n" {
		t.Fatalf("ps1 meta = %+v", ps1)
	}
	reg := defaultTextMeta(`C:\tmp\a.reg`)
	if reg.Enc != textUTF16LE || !reg.BOM || reg.EOL != "\r\n" {
		t.Fatalf("reg meta = %+v", reg)
	}
	goFile := defaultTextMeta(`C:\tmp\a.go`)
	if goFile.Enc != textUTF8 || goFile.BOM || goFile.EOL != "\n" {
		t.Fatalf("go meta = %+v", goFile)
	}
	txt := defaultTextMeta(`C:\tmp\a.txt`)
	if txt.Enc != textUTF8 || txt.BOM || txt.EOL != "" {
		t.Fatalf("txt meta = %+v", txt)
	}
}

func TestReadFileShowsEncodingAndNormalizesEOL(t *testing.T) {
	t.Setenv("WIN7HARNESS_PORTABLE", "1")
	dir := t.TempDir()
	p := filepath.Join(dir, "a.c")
	raw, err := encodeText("第一行\r\n第二行\r\n", textMeta{Enc: textGB18030, EOL: "\r\n"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := runReadFile(&Context{WorkDir: dir}, map[string]interface{}{"path": "a.c"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "第一行") || !strings.Contains(res.Output, "gb18030") {
		t.Fatalf("output = %q", res.Output)
	}
	if strings.Contains(res.Output, "\r") {
		t.Fatalf("read_file should normalize CRLF for display: %q", res.Output)
	}
}

func TestEditFileMatchesLFAndPreservesGBKCRLF(t *testing.T) {
	t.Setenv("WIN7HARNESS_PORTABLE", "1")
	dir := t.TempDir()
	p := filepath.Join(dir, "a.c")
	raw, err := encodeText("第一行\r\n第二行\r\n", textMeta{Enc: textGB18030, EOL: "\r\n"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// 模型只看到 LF，old_string 里不会有 \r。
	if _, err := runEditFile(&Context{WorkDir: dir}, map[string]interface{}{
		"path": "a.c", "old_string": "第二行", "new_string": "第三行",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text, meta, err := decodeTextBytes(got)
	if err != nil {
		t.Fatal(err)
	}
	if text != "第一行\r\n第三行\r\n" {
		t.Fatalf("text = %q", text)
	}
	if meta.Enc != textGB18030 || meta.EOL != "\r\n" {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestWriteFileNewBatUsesGBKCRLF(t *testing.T) {
	t.Setenv("WIN7HARNESS_PORTABLE", "1")
	dir := t.TempDir()
	if _, err := runWriteFile(&Context{WorkDir: dir}, map[string]interface{}{
		"path": "run.bat", "content": "@echo off\necho 中文\n",
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "run.bat"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("batch file must not start with UTF-8 BOM")
	}
	text, meta, err := decodeTextBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if text != "@echo off\r\necho 中文\r\n" {
		t.Fatalf("text = %q", text)
	}
	if meta.Enc != textGB18030 || meta.EOL != "\r\n" {
		t.Fatalf("meta = %+v", meta)
	}
}
