package tools

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// Win7 老项目里最常见的不是 UTF-8，而是 GBK/GB18030 + CRLF，偶尔还有
// UTF-16LE（.reg、某些 IDE 导出的文件）和带 BOM 的 UTF-8。
//
// 这一层只做一件事：读进来统一成 UTF-8 文本，写回去还原原来的编码、
// BOM 和换行。模型永远只看到 UTF-8；磁盘上的字节尽量不变。
// 这样 edit_file/apply_patch 不会因为 \r\n 或 GBK 而匹配失败，
// 也不会把老文件整篇改写成 UTF-8/LF 造成全量 diff。

type textEncoding string

const (
	textUTF8    textEncoding = "utf-8"
	textUTF16LE textEncoding = "utf-16le"
	textUTF16BE textEncoding = "utf-16be"
	textGB18030 textEncoding = "gb18030"
	textCP1252  textEncoding = "windows-1252"
)

type textMeta struct {
	Enc textEncoding
	BOM bool
	EOL string // "\r\n" 或 "\n"
}

func (m textMeta) label() string {
	enc := string(m.Enc)
	if m.BOM {
		enc += " BOM"
	}
	eol := "LF"
	if m.EOL == "\r\n" {
		eol = "CRLF"
	}
	return enc + ", " + eol
}

// decodeTextBytes 把磁盘字节解成 UTF-8 文本。第二个返回值描述原编码，
// 写回时用它还原。二进制文件返回 errBinaryText。
func decodeTextBytes(data []byte) (string, textMeta, error) {
	meta := textMeta{Enc: textUTF8, EOL: defaultEOL()}
	if len(data) == 0 {
		return "", meta, nil
	}

	// 先处理 BOM。UTF-32 的 BOM 与 UTF-16LE 前缀相同，必须排在前面排除。
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		meta.Enc, meta.BOM = textUTF8, true
		data = data[3:]
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		if len(data) >= 4 && data[2] == 0 && data[3] == 0 {
			return "", meta, fmt.Errorf("暂不支持 UTF-32LE 文本（%w）", errBinaryText)
		}
		meta.Enc, meta.BOM = textUTF16LE, true
		data = data[2:]
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		meta.Enc, meta.BOM = textUTF16BE, true
		data = data[2:]
	}

	if meta.Enc == textUTF16LE || meta.Enc == textUTF16BE {
		text, err := decodeUTF16(data, meta.Enc == textUTF16BE)
		if err != nil {
			return "", meta, err
		}
		meta.EOL = detectEOL(text)
		return text, meta, nil
	}

	// 没有 BOM 的 UTF-16：只认“ASCII 字符高位全 0”的强特征，
	// 避免把普通二进制误判成 UTF-16。
	if enc, ok := sniffUTF16(data); ok {
		text, err := decodeUTF16(data, enc == textUTF16BE)
		if err != nil {
			return "", meta, err
		}
		meta.Enc, meta.BOM, meta.EOL = enc, false, detectEOL(text)
		return text, meta, nil
	}

	if looksBinary(data) {
		return "", meta, errBinaryText
	}

	if utf8.Valid(data) {
		text := string(data)
		meta.Enc, meta.EOL = textUTF8, detectEOL(text)
		return text, meta, nil
	}

	// GB18030 覆盖 GBK/GB2312，是中文 Win7 最常见的非 UTF-8 编码。
	// 注意：x/text 的 decoder 对无效字节有时会返回 U+FFFD 而不是 error，
	// 所以还要检查替换字符，避免把 CP1252 的 café 解成 caf�。
	if text, _, err := transform.String(simplifiedchinese.GB18030.NewDecoder(), string(data)); err == nil && !strings.ContainsRune(text, utf8.RuneError) {
		meta.Enc, meta.EOL = textGB18030, detectEOL(text)
		return text, meta, nil
	}

	// 英文 Win7 的老文件可能是 CP1252。它几乎不会解码失败，
	// 所以放在 GB18030 之后，避免把中文 GBK 抢走。
	if text, _, err := transform.String(charmap.Windows1252.NewDecoder(), string(data)); err == nil {
		meta.Enc, meta.EOL = textCP1252, detectEOL(text)
		return text, meta, nil
	}

	return "", meta, fmt.Errorf("无法识别文本编码（可能是二进制文件）：%w", errBinaryText)
}

var errBinaryText = fmt.Errorf("二进制文件")

func decodeUTF16(data []byte, be bool) (string, error) {
	if len(data)%2 != 0 {
		return "", fmt.Errorf("UTF-16 字节数为奇数，文件可能已损坏")
	}
	u := make([]uint16, len(data)/2)
	for i := range u {
		if be {
			u[i] = binary.BigEndian.Uint16(data[i*2 : i*2+2])
		} else {
			u[i] = binary.LittleEndian.Uint16(data[i*2 : i*2+2])
		}
	}
	return string(utf16.Decode(u)), nil
}

// sniffUTF16 只接受非常明确的 ASCII UTF-16 特征：
// LE 的奇数字节几乎全是 0，BE 的偶数字节几乎全是 0。
func sniffUTF16(data []byte) (textEncoding, bool) {
	// 少于 8 字节的样本歧义太大（00 01 02 03 会被误判成 UTF-16BE），
	// 直接当二进制/未知编码处理，让调用方走报错或 CP1252 兜底。
	if len(data) < 8 || len(data)%2 != 0 {
		return "", false
	}
	var zeroEven, zeroOdd int
	for i, b := range data {
		if b != 0 {
			continue
		}
		if i%2 == 0 {
			zeroEven++
		} else {
			zeroOdd++
		}
	}
	half := len(data) / 2
	if zeroOdd >= half*3/4 && zeroEven <= half/8 {
		return textUTF16LE, true
	}
	if zeroEven >= half*3/4 && zeroOdd <= half/8 {
		return textUTF16BE, true
	}
	return "", false
}

func looksBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	// 常见二进制格式即使没有 NUL 也不该进模型上下文。
	for _, magic := range [][]byte{
		{0x89, 'P', 'N', 'G'},
		{0xFF, 0xD8, 0xFF},       // JPEG
		{'%', 'P', 'D', 'F'},     // PDF
		{'P', 'K', 0x03, 0x04},   // ZIP/docx/xlsx
		{0x1F, 0x8B},             // gzip
		{'M', 'Z'},               // PE
		{0x7F, 'E', 'L', 'F'},    // ELF
		{0xD0, 0xCF, 0x11, 0xE0}, // OLE (doc/xls)
	} {
		if bytes.HasPrefix(data, magic) {
			return true
		}
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	ctrl := 0
	for _, b := range data {
		switch b {
		case '\t', '\n', '\r', '\f', '\b':
			continue
		}
		if b < 0x20 {
			ctrl++
		}
	}
	// 2% 以上控制字符基本可以判定为二进制；正常源码里极少见。
	return ctrl*100/len(data) > 2
}

func detectEOL(s string) string {
	crlf := strings.Count(s, "\r\n")
	lf := strings.Count(s, "\n")
	if crlf > 0 && crlf*2 >= lf {
		return "\r\n"
	}
	return "\n"
}

func defaultEOL() string {
	if isWindows() {
		return "\r\n"
	}
	return "\n"
}

// encodeText 把 UTF-8 文本按 meta 还原成磁盘字节。
// 输入里的 \r\n、\r 会先归一成 \n，再统一换成 meta.EOL；
// 这样 edit_file 可以直接用 LF 匹配，不会破坏 CRLF 文件。
func encodeText(text string, meta textMeta) ([]byte, error) {
	text = normalizeEOL(text)
	if meta.EOL == "" {
		meta.EOL = "\n"
	}
	if meta.EOL != "\n" {
		text = strings.ReplaceAll(text, "\n", meta.EOL)
	}

	switch meta.Enc {
	case "", textUTF8:
		out := []byte(text)
		if meta.BOM {
			out = append([]byte{0xEF, 0xBB, 0xBF}, out...)
		}
		return out, nil
	case textUTF16LE, textUTF16BE:
		u := utf16.Encode([]rune(text))
		out := make([]byte, 0, len(u)*2+2)
		if meta.BOM {
			if meta.Enc == textUTF16BE {
				out = append(out, 0xFE, 0xFF)
			} else {
				out = append(out, 0xFF, 0xFE)
			}
		}
		for _, v := range u {
			var b [2]byte
			if meta.Enc == textUTF16BE {
				binary.BigEndian.PutUint16(b[:], v)
			} else {
				binary.LittleEndian.PutUint16(b[:], v)
			}
			out = append(out, b[:]...)
		}
		return out, nil
	case textGB18030:
		out, _, err := transform.String(simplifiedchinese.GB18030.NewEncoder(), text)
		if err != nil {
			return nil, fmt.Errorf("原文是 GBK/GB18030，新内容含无法编码的字符（如 emoji）；请改用 UTF-8 文件或删除该字符：%v", err)
		}
		// x/text 的 GB18030 encoder 遇到 emoji 不报错，而是写成 '?'；
		// 必须回读比较，否则会在用户不知情时把内容改坏。
		if round, _, derr := transform.String(simplifiedchinese.GB18030.NewDecoder(), out); derr != nil || round != text {
			return nil, fmt.Errorf("原文是 GBK/GB18030，新内容含无法编码的字符（如 emoji）；请改用 UTF-8 文件或删除该字符")
		}
		return []byte(out), nil
	case textCP1252:
		out, _, err := transform.String(charmap.Windows1252.NewEncoder(), text)
		if err != nil {
			return nil, fmt.Errorf("原文是 Windows-1252，新内容含无法编码的字符；请改用 UTF-8 文件：%v", err)
		}
		if round, _, derr := transform.String(charmap.Windows1252.NewDecoder(), out); derr != nil || round != text {
			return nil, fmt.Errorf("原文是 Windows-1252，新内容含无法编码的字符；请改用 UTF-8 文件")
		}
		return []byte(out), nil
	default:
		return nil, fmt.Errorf("未知文本编码：%s", meta.Enc)
	}
}

func normalizeEOL(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func readTextFile(path string) (string, textMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", textMeta{}, err
	}
	return decodeTextBytes(data)
}

// defaultTextMeta 只用于新建文件。已有文件一律保留原编码。
func defaultTextMeta(path string) textMeta {
	ext := strings.ToLower(filepath.Ext(path))
	meta := textMeta{Enc: textUTF8, EOL: ""}
	switch ext {
	case ".bat", ".cmd":
		// cmd.exe 在中文 Win7 上按 OEM 936 读批处理；UTF-8 中文会乱码，
		// UTF-8 BOM 甚至会让第一行变成非法命令。GB18030 与 GBK 兼容。
		meta.Enc, meta.EOL = textGB18030, "\r\n"
	case ".ps1":
		// PowerShell 5.1 读取无 BOM 的 UTF-8 时会按 ANSI 解码。
		meta.Enc, meta.BOM, meta.EOL = textUTF8, true, "\r\n"
	case ".reg":
		// regedit 在 Win7 上只认 UTF-16LE BOM 的 Unicode .reg。
		meta.Enc, meta.BOM, meta.EOL = textUTF16LE, true, "\r\n"
	case ".txt", ".md", ".log":
		// 不默认加 BOM：BOM 会进入配置文件/评测断言，也会干扰 grep、
		// 正则首行匹配。需要给 Win7 记事本看的中文文档，由发布脚本
		// 单独加 BOM（build.sh 的 README 就是这么做的）。
		// EOL 留空表示“跟随写入内容”，避免把 LF 脚本/测试数据强行改成 CRLF。
		meta.Enc, meta.EOL = textUTF8, ""
	case ".go", ".c", ".h", ".cc", ".cpp", ".cxx", ".cs", ".java",
		".js", ".jsx", ".ts", ".tsx", ".py", ".rs", ".rb", ".php",
		".json", ".yaml", ".yml", ".toml", ".xml", ".html", ".css", ".scss":
		meta.Enc, meta.EOL = textUTF8, "\n"
	}
	return meta
}

// detectTextForWrite 在覆盖已有文件时读取原编码；读不到（新文件）就用扩展名默认值。
func detectTextForWrite(path string) (textMeta, bool) {
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		if text, meta, derr := readTextFile(path); derr == nil {
			_ = text
			return meta, true
		}
	}
	return defaultTextMeta(path), false
}

// normalizeForMatch 把模型给的 old_string/new_string 归一成 LF，
// 这样 CRLF 文件也能用普通 \n 精确匹配。
func normalizeForMatch(s string) string {
	return normalizeEOL(s)
}

func isWindows() bool {
	return os.PathSeparator == '\\'
}
