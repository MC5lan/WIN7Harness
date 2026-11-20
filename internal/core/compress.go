package core

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

// 会话与日志类 JSON 的压缩工具。
// 长对话的正文里重复度很高（字段名、工具输出、代码片段），gzip 通常能压到 1/5 左右，
// 对 32 位进程来说既省磁盘也省装载时的内存峰值。
// 这里只做"压了确实更小"的判断，避免小文件为了压缩反而变大、还多一次解压开销。

// IsGzipData 判断数据是否为 gzip 流（魔数 1f 8b）。
func IsGzipData(data []byte) bool {
	return len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b
}

// GzipIfSmaller 对超过 min 字节的数据做 gzip，返回 nil 表示"不建议压缩/压缩无收益"。
func GzipIfSmaller(data []byte, min int) []byte {
	if min < 0 {
		min = 0
	}
	if len(data) < min {
		return nil
	}
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil
	}
	if _, err := zw.Write(data); err != nil {
		return nil
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	if buf.Len() >= len(data) {
		return nil
	}
	return buf.Bytes()
}

// GunzipData 解压 gzip 数据；非 gzip 原样返回。
// maxBytes 为解压后上限（0 表示用默认 128MB），挡住畸形文件把内存吃光。
func GunzipData(data []byte, maxBytes int64) ([]byte, error) {
	if !IsGzipData(data) {
		return data, nil
	}
	if maxBytes <= 0 {
		maxBytes = 128 << 20
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(out)) > maxBytes {
		return nil, fmt.Errorf("解压后超过上限 %d 字节", maxBytes)
	}
	return out, nil
}
