// qrdump 打印二维码矩阵，供 tools/qr_crosscheck.py 与标准实现逐位对拍。
//
// 用法：qrdump <text> [mask]     mask 省略或负数表示按罚分自动选。
package main

import (
	"fmt"
	"os"
	"strconv"

	"win7harness/internal/qr"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: qrdump <text> [mask]")
		os.Exit(2)
	}
	text := os.Args[1]
	force := -1
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil {
			force = v
		}
	}
	m, err := qr.EncodeMask(text, qr.LevelM, force)
	if err != nil {
		fmt.Fprintln(os.Stderr, "编码失败:", err)
		os.Exit(1)
	}
	fmt.Printf("version=%d mask=%d size=%d\n", m.Version, m.Mask, m.Size)
	for y := 0; y < m.Size; y++ {
		for x := 0; x < m.Size; x++ {
			if m.At(x, y) {
				fmt.Print("1")
			} else {
				fmt.Print("0")
			}
		}
		fmt.Println()
	}
}
