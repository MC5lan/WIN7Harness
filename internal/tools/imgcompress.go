package tools

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
)

// 图片压缩：解码走标准库，重采样用 x/image/draw 的 CatmullRom（已在依赖中，无新增）。
// 目的只有一个——把手机截图/设计稿这类 3~8MB 的大图降到 100~400KB，
// 既不撑爆请求体，也不白白多花 token。
//
// 缩放算法从手写面积平均换成 CatmullRom：同样是纯 Go，但文字/细线边缘更干净，
// 且省掉 40 行逐像素循环。x/image 是 canvas 渲染内核的既有依赖，不引入新风险。
// 支持 jpeg / png / gif；bmp / webp 标准库解不了，原样透传（交给视觉模型）。

const (
	shrinkDefaultSide = 1568 // 与主流视觉模型的最佳边长接近
	shrinkQuality     = 82
)

// shrinkImage 把图片按最长边限制缩放并重编码为 JPEG。
// 返回 ok=false 表示格式不受支持或无需压缩。
func shrinkImage(data []byte, mime string, maxSide int) (out []byte, outMime string, ow, oh, nw, nh int, ok bool, err error) {
	if maxSide <= 0 {
		maxSide = shrinkDefaultSide
	}
	var src image.Image
	needResize := false

	switch mime {
	case "image/jpeg":
		src, err = jpeg.Decode(bytes.NewReader(data))
	case "image/png":
		src, err = png.Decode(bytes.NewReader(data))
	case "image/gif":
		src, err = gif.Decode(bytes.NewReader(data))
	default:
		return nil, mime, 0, 0, 0, 0, false, nil
	}
	if err != nil {
		return nil, mime, 0, 0, 0, 0, false, fmt.Errorf("解码失败：%v", err)
	}
	b := src.Bounds()
	ow, oh = b.Dx(), b.Dy()
	if ow <= 0 || oh <= 0 {
		return nil, mime, 0, 0, 0, 0, false, fmt.Errorf("图片尺寸异常")
	}

	nw, nh = ow, oh
	if longest := max(ow, oh); longest > maxSide {
		scale := float64(maxSide) / float64(longest)
		nw = max(1, int(float64(ow)*scale))
		nh = max(1, int(float64(oh)*scale))
		needResize = true
	}

	// 缩了尺寸就直接转 JPEG（截图类体积能降一个数量级）；
	// 没缩但原图是 PNG 且大于 600KB 时也转 JPEG，效果同样明显。
	if !needResize && (mime != "image/png" || len(data) < 600*1024) {
		return nil, mime, ow, oh, ow, oh, false, nil
	}

	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	if !needResize {
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	} else {
		// CatmullRom 重采样：边缘比手写面积平均更锐利，文字截图缩小后更清晰。
		// draw.Src 直接覆盖目标；后续 flatten 仍把 transparent 像素铺到白底。
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flatten(dst), &jpeg.Options{Quality: shrinkQuality}); err != nil {
		return nil, mime, ow, oh, nw, nh, false, fmt.Errorf("编码失败：%v", err)
	}
	// 没缩尺寸时，压完反而更大（小图转 JPEG 常见）就放弃。
	// 缩过尺寸则必须生效：像素少了就是省 token，体积略增也认。
	if !needResize && buf.Len() >= len(data) {
		return nil, mime, ow, oh, ow, oh, false, nil
	}
	return buf.Bytes(), "image/jpeg", ow, oh, nw, nh, true, nil
}

// flatten 把带 alpha 的图铺到白底上。
func flatten(img *image.RGBA) image.Image {
	white := image.NewRGBA(img.Bounds())
	draw.Draw(white, white.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(white, white.Bounds(), img, image.Point{}, draw.Over)
	return white
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// compressHint 生成给人看的一句话说明。
func compressHint(ow, oh, nw, nh int, before, after int) string {
	if ow == nw && oh == nh {
		return fmt.Sprintf("重编码为 JPEG：%s → %s", humanBytes(before), humanBytes(after))
	}
	return fmt.Sprintf("%d×%d → %d×%d，%s → %s", ow, oh, nw, nh, humanBytes(before), humanBytes(after))
}

func humanBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.0fKB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
}
