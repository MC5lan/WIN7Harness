package tools

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"win7harness/internal/core"
	"win7harness/internal/llm"
)

// readImageMaxBytes 单张图上限：超过就只报元信息，避免把进程内存与上下文打爆。
const readImageMaxBytes = 8 << 20

func init() {
	register(Tool{
		Name: "read_image",
		Desc: "读取本地图片（png/jpg/jpeg/gif/bmp/webp）交给模型直接查看：截图、设计稿、报错界面、图表都能看。" +
			"大图会自动压到长边 1568 像素再发送（省带宽省 token）；ocr=true 时额外调用本地 tesseract 做文字识别，" +
			"适合「只要文字、不需要看图」的场景（如整页合同、表格截图）。",
		Schema: `{
  "type":"object",
  "properties":{
    "path":{"type":"string","description":"图片文件路径"},
    "detail":{"type":"string","description":"清晰度：auto(默认) / low(省 token) / high(看细节)"},
    "max_side":{"type":"integer","description":"压缩后最长边像素，默认 1568；传 0 表示不压缩原样发送"},
    "ocr":{"type":"boolean","description":"是否调用本地 tesseract 做文字识别，默认 false"},
    "ocr_lang":{"type":"string","description":"OCR 语言，默认 chi_sim+eng"},
    "save_compressed":{"type":"string","description":"可选：把压缩后的图片另存到该路径（相对工作区或绝对路径）"}
  },
  "required":["path"]
}`,
		Run: runReadImage,
	})
}

func runReadImage(ctx *Context, args map[string]interface{}) (Result, error) {
	p := core.ResolvePath(ctx.WorkDir, Get(args, "path"))
	if err := core.ValidateWindowsPathSyntax(p); err != nil {
		return Result{}, err
	}
	if err := guardEscapes(ctx, p, "读取"); err != nil {
		return Result{}, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return Result{}, fmt.Errorf("图片不存在: %s", p)
	}
	if st.IsDir() {
		return Result{}, fmt.Errorf("%s 是目录，请给出图片文件路径", p)
	}
	mime := imageMime(p)
	if mime == "" {
		return Result{}, fmt.Errorf("不支持的图片格式：%s（支持 png/jpg/jpeg/gif/bmp/webp）", filepath.Ext(p))
	}
	// 只做 OCR 时不受体积上限约束：图片不进上下文，只取文字。
	// OCR 走文件路径直接喂给 tesseract，不需要把整个文件读进内存；
	// 之前的实现无论哪个分支都 os.ReadFile，32 位进程会被大图撑爆。
	wantOCR := GetBool(args, "ocr", false)
	big := st.Size() > readImageMaxBytes
	if big && !wantOCR {
		return Result{}, fmt.Errorf("图片过大（%s），上限 %s；请先压缩或裁剪后再读（或只做 OCR：ocr=true）",
			core.HumanSize(st.Size()), core.HumanSize(readImageMaxBytes))
	}
	var data []byte
	if !big {
		data, err = os.ReadFile(p)
		if err != nil {
			return Result{}, fmt.Errorf("读取图片失败：%v", err)
		}
	}
	w, h := imageSize(data)

	var b strings.Builder
	fmt.Fprintf(&b, "已读取图片 %s\n格式：%s，体积：%s", filepath.Base(p), strings.TrimPrefix(mime, "image/"), core.HumanSize(st.Size()))
	if w > 0 && h > 0 {
		fmt.Fprintf(&b, "，尺寸：%d×%d", w, h)
	}
	if data == nil {
		fmt.Fprintf(&b, "\n图片过大（>%s），仅做 OCR，不读入内存、不附带图片内容", core.HumanSize(readImageMaxBytes))
	}

	// 大图先压：视觉模型吃 4MB 和吃 300KB 看到的细节几乎没差别，但请求更稳、更便宜。
	outData, outMime := data, mime
	if data != nil {
		if maxSide := GetInt(args, "max_side", shrinkDefaultSide); maxSide > 0 {
			if shrunk, sm, ow, oh, nw, nh, ok, cerr := shrinkImage(data, mime, maxSide); cerr != nil {
				core.Logf("图片压缩失败，按原图发送：%v", cerr)
			} else if ok {
				outData, outMime = shrunk, sm
				fmt.Fprintf(&b, "\n压缩：%s", compressHint(ow, oh, nw, nh, len(data), len(shrunk)))
				if savePath := strings.TrimSpace(Get(args, "save_compressed")); savePath != "" {
					sp := core.ResolvePath(ctx.WorkDir, savePath)
					// 另存必须过与 write_file 相同的防线：系统保护目录硬拒绝、
					// 工作区外需确认（fail-closed）、快照留底、原子写入。
					if err := core.ValidateWindowsPathSyntax(sp); err != nil {
						fmt.Fprintf(&b, "\n（另存失败：%v）", err)
					} else if other, ok := core.CaseInsensitiveCollision(sp); ok {
						fmt.Fprintf(&b, "\n（另存失败：与已有文件仅大小写不同：%s）", other)
					} else if core.IsForbiddenPath(sp) {
						fmt.Fprintf(&b, "\n（另存失败：目标路径属于系统保护目录：%s）", sp)
					} else if err := guardEscapes(ctx, sp, "写入"); err != nil {
						fmt.Fprintf(&b, "\n（另存失败：%v）", err)
					} else if err := core.SnapshotFile(sp); err != nil {
						fmt.Fprintf(&b, "\n（另存失败：写前快照失败：%v）", err)
					} else if err := core.WriteFileAtomic(sp, shrunk, 0o644); err != nil {
						fmt.Fprintf(&b, "\n（另存失败：%v）", err)
					} else {
						fmt.Fprintf(&b, "\n已另存：%s", sp)
					}
				}
			}
		} else if st.Size() > readImageMaxBytes {
			return Result{}, fmt.Errorf("要求不压缩原样发送，但图片 %s 超过上限 %s；请降低 max_side 或先裁剪",
				core.HumanSize(st.Size()), core.HumanSize(readImageMaxBytes))
		}
	}

	if wantOCR {
		text, oerr := ocrImage(p, Get(args, "ocr_lang"), 0)
		if oerr != nil {
			fmt.Fprintf(&b, "\n\nOCR 未完成：%v", oerr)
		} else if text == "" {
			b.WriteString("\n\nOCR 结果：未识别到文字（图片可能是纯图形或文字太小）")
		} else {
			fmt.Fprintf(&b, "\n\nOCR 文字（%d 字）：\n%s", len([]rune(text)), clipForTool(text, 8000))
		}
	}

	if outData == nil {
		// 大图仅 OCR：未读入内存，没有可附带的字节。
		return Result{Output: b.String(), Title: "读取图片"}, nil
	}
	if wantOCR && !GetBool(args, "attach", true) {
		return Result{Output: b.String(), Title: "读取图片"}, nil
	}
	if dataURL := llm.ImageDataURL(outMime, outData); dataURL == "" {
		fmt.Fprintf(&b, "\n（图片编码后超过 12MB，已丢弃——请先压缩）")
	} else {
		b.WriteString("\n（图片内容已随本次结果发送给模型）\n")
		b.WriteString("[[img:" + dataURL + "]]")
	}
	return Result{Output: b.String(), Title: "读取图片"}, nil
}

// clipForTool 截断超长文本，明确标注省略量，避免模型误以为这就是全部内容。
func clipForTool(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + fmt.Sprintf("\n…（已省略 %d 字）", len(r)-max)
}

// imageMime 按扩展名判定 MIME，返回空表示不支持。
func imageMime(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	case ".webp":
		return "image/webp"
	}
	return ""
}

// imageSize 解析常见格式的宽高；识别不了返回 0,0（不影响读图，仅少一行元信息）。
func imageSize(b []byte) (int, int) {
	if len(b) < 24 {
		return 0, 0
	}
	// PNG：IHDR 固定在 16..24 字节
	if b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G' && len(b) >= 24 {
		w := int(binary.BigEndian.Uint32(b[16:20]))
		h := int(binary.BigEndian.Uint32(b[20:24]))
		return w, h
	}
	// GIF：逻辑屏幕宽高，小端 16 位
	if b[0] == 'G' && b[1] == 'I' && b[2] == 'F' && len(b) >= 10 {
		w := int(binary.LittleEndian.Uint16(b[6:8]))
		h := int(binary.LittleEndian.Uint16(b[8:10]))
		return w, h
	}
	// BMP
	if b[0] == 'B' && b[1] == 'M' && len(b) >= 26 {
		w := int(int32(binary.LittleEndian.Uint32(b[18:22])))
		h := int(int32(binary.LittleEndian.Uint32(b[22:26])))
		if h < 0 {
			h = -h
		}
		return w, h
	}
	// JPEG：扫描 SOF0/SOF2 段
	if b[0] == 0xFF && b[1] == 0xD8 {
		i := 2
		for i+9 < len(b) {
			if b[i] != 0xFF {
				i++
				continue
			}
			marker := b[i+1]
			if marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) {
				i += 2
				continue
			}
			segLen := int(binary.BigEndian.Uint16(b[i+2 : i+4]))
			if segLen < 2 {
				return 0, 0
			}
			isSOF := marker >= 0xC0 && marker <= 0xCE && marker != 0xC4 && marker != 0xC8 && marker != 0xCC
			if isSOF && i+9 < len(b) {
				h := int(binary.BigEndian.Uint16(b[i+5 : i+7]))
				w := int(binary.BigEndian.Uint16(b[i+7 : i+9]))
				return w, h
			}
			i += 2 + segLen
		}
	}
	return 0, 0
}
