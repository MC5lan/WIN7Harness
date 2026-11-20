// Package qr 生成二维码矩阵。
//
// 为什么自己写：本项目零第三方依赖、单文件分发，引一个二维码库会把构建链
// 绑到网络与 vendor 上；而需求只有"把配对地址编成二维码给手机扫"，
// 用不上结构化附加、Micro QR、Kanji 模式。
//
// 实现范围：字节模式、纠错等级 M、版本 1~10（最多 213 字节，够放配对 URL）、
// 8 种掩码按规范罚分择优。生成的矩阵与原版 QR 规范一致，
// 自检里用 Python segno 产出的矩阵逐位对拍（见 tools/qr_crosscheck.py）。
package qr

import (
	"errors"
	"fmt"
	"strings"
)

// Level 纠错等级。目前只用 M（约 15% 容错），扫码在屏幕上足够。
type Level int

// 纠错等级取值，顺序与格式信息编码一致。
const (
	LevelL Level = 0
	LevelM Level = 1
	LevelQ Level = 2
	LevelH Level = 3
)

// ecLevelBits 格式信息里的纠错等级位（L=01, M=00, Q=11, H=10）。
func (l Level) bits() int {
	switch l {
	case LevelL:
		return 1
	case LevelM:
		return 0
	case LevelQ:
		return 3
	default:
		return 2
	}
}

// blockSpec 一个版本在 M 级下的纠错块结构。
// data 是每块的数据码字数，blocks 是该组的块数。
type blockSpec struct {
	ec       int   // 每块的纠错码字数
	blocks   []int // 各组的块数
	dataLens []int // 各组每块的数据码字数
}

// specs 版本 1~10 在 M 级下的块结构（ISO/IEC 18004 表 9）。
var specs = map[int]blockSpec{
	1:  {ec: 10, blocks: []int{1}, dataLens: []int{16}},
	2:  {ec: 16, blocks: []int{1}, dataLens: []int{28}},
	3:  {ec: 26, blocks: []int{1}, dataLens: []int{44}},
	4:  {ec: 18, blocks: []int{2}, dataLens: []int{32}},
	5:  {ec: 24, blocks: []int{2}, dataLens: []int{43}},
	6:  {ec: 16, blocks: []int{4}, dataLens: []int{27}},
	7:  {ec: 18, blocks: []int{4}, dataLens: []int{31}},
	8:  {ec: 22, blocks: []int{2, 2}, dataLens: []int{38, 39}},
	9:  {ec: 22, blocks: []int{3, 2}, dataLens: []int{36, 37}},
	10: {ec: 26, blocks: []int{4, 1}, dataLens: []int{43, 44}},
}

// alignPos 各版本对齐图案中心坐标。
var alignPos = map[int][]int{
	1:  nil,
	2:  {6, 18},
	3:  {6, 22},
	4:  {6, 26},
	5:  {6, 30},
	6:  {6, 34},
	7:  {6, 22, 38},
	8:  {6, 24, 42},
	9:  {6, 26, 46},
	10: {6, 28, 50},
}

// Matrix 二维码矩阵，true 表示深色模块。
type Matrix struct {
	Size    int
	Modules [][]bool
	Version int
	Mask    int
}

// At 读一个模块，越界返回 false。
func (m *Matrix) At(x, y int) bool {
	if m == nil || y < 0 || y >= m.Size || x < 0 || x >= m.Size {
		return false
	}
	return m.Modules[y][x]
}

// Encode 把文本编成二维码矩阵。
// Encode 生成 QR 矩阵。
// 目前只支持 LevelM——其它纠错等级没有实现块结构表，显式报错而不是按 M 静默生成。
func Encode(text string, level Level) (*Matrix, error) {
	if level != LevelM {
		return nil, fmt.Errorf("QR 目前只支持 LevelM（其它纠错级别未实现块结构表）")
	}
	return EncodeMask(text, level, -1)
}

// Codewords 返回版本与最终码字（数据 + 纠错，已按块交错），仅供自检与对拍。
func Codewords(text string, level Level) (int, []byte, error) {
	if level != LevelM {
		return 0, nil, fmt.Errorf("QR 目前只支持 LevelM（其它纠错级别未实现块结构表）")
	}
	ver, _, all, err := buildCodewords(text, level)
	return ver, all, err
}

// buildCodewords 完成"模式编码 → 补齐 → 加纠错 → 交错"。
func buildCodewords(text string, level Level) (int, blockSpec, []byte, error) {
	data := []byte(text)
	ver, spec, err := pickVersion(len(data), level)
	if err != nil {
		return 0, blockSpec{}, nil, err
	}
	bits := encodeData(data, ver, spec, level)
	codewords := bitsToCodewords(bits)
	if len(codewords) != totalDataCodewords(spec) {
		return 0, blockSpec{}, nil, fmt.Errorf("数据码字数不符：%d != %d", len(codewords), totalDataCodewords(spec))
	}
	return ver, spec, addEC(codewords, spec, ver), nil
}

// BaseMatrix 返回未加掩码、未写格式信息的矩阵，仅用于对拍定位。
func BaseMatrix(text string, level Level) (*Matrix, error) {
	ver, _, all, err := buildCodewords(text, level)
	if err != nil {
		return nil, err
	}
	base := newMatrix(ver)
	placeFunctionPatterns(base, ver)
	placeData(base, all, ver)
	return &Matrix{Size: len(base), Modules: base, Version: ver, Mask: -1}, nil
}

// ReservedMap 返回功能区标记（true 表示不可放数据），仅用于对拍定位。
func ReservedMap(ver int) [][]bool { return reserved(ver) }

// MaskBit 暴露掩码条件，仅用于对拍定位。
func MaskBit(mask, x, y int) bool { return maskFn(mask, x, y) }

// EncodeMask 同 Encode，但可强制指定掩码（0~7）；force < 0 表示按罚分自动选。
// 强制掩码只给自检对拍用，正常调用走 Encode。
func EncodeMask(text string, level Level, force int) (*Matrix, error) {
	if level != LevelM {
		return nil, fmt.Errorf("QR 目前只支持 LevelM（其它纠错级别未实现块结构表）")
	}
	ver, _, all, err := buildCodewords(text, level)
	if err != nil {
		return nil, err
	}

	base := newMatrix(ver)
	placeFunctionPatterns(base, ver)
	placeData(base, all, ver)

	if force >= 0 && force <= 7 {
		m := cloneMatrix(base)
		applyMask(m, force)
		drawFormat(m, level, force)
		if ver >= 7 {
			drawVersion(m, ver)
		}
		return &Matrix{Size: len(m), Modules: m, Version: ver, Mask: force}, nil
	}

	best := -1
	var bestScore int
	var bestM [][]bool
	for mask := 0; mask < 8; mask++ {
		m := cloneMatrix(base)
		applyMask(m, mask)
		drawFormat(m, level, mask)
		if ver >= 7 {
			drawVersion(m, ver)
		}
		score := penalty(m)
		if best < 0 || score < bestScore {
			best, bestScore, bestM = mask, score, m
		}
	}
	return &Matrix{Size: len(bestM), Modules: bestM, Version: ver, Mask: best}, nil
}

// pickVersion 选能装下数据的最小版本（考虑字符计数位与终止符）。
func pickVersion(n int, level Level) (int, blockSpec, error) {
	for v := 1; v <= 10; v++ {
		sp := specs[v]
		capacity := totalDataCodewords(sp) * 8
		countBits := 8
		if v >= 10 {
			countBits = 16
		}
		need := 4 + countBits + n*8
		if need <= capacity {
			return v, sp, nil
		}
	}
	return 0, blockSpec{}, errors.New("内容过长：最多 213 字节")
}

func totalDataCodewords(sp blockSpec) int {
	n := 0
	for i, b := range sp.blocks {
		n += b * sp.dataLens[i]
	}
	return n
}

// encodeData 生成数据位流：模式 + 计数 + 数据 + 终止符 + 补齐。
func encodeData(data []byte, ver int, sp blockSpec, level Level) []byte {
	var out []byte
	put := func(v, n int) {
		for i := n - 1; i >= 0; i-- {
			out = append(out, byte((v>>i)&1))
		}
	}
	put(0b0100, 4) // 字节模式
	if ver >= 10 {
		put(len(data), 16)
	} else {
		put(len(data), 8)
	}
	for _, b := range data {
		put(int(b), 8)
	}

	capacity := totalDataCodewords(sp) * 8
	// 终止符最多 4 位，装不下就截断。
	term := 4
	if capacity-len(out) < term {
		term = capacity - len(out)
	}
	put(0, term)
	for len(out)%8 != 0 {
		out = append(out, 0)
	}
	// 交替填充 0xEC / 0x11 直到填满。
	pad := []byte{0xEC, 0x11}
	for i := 0; len(out) < capacity; i++ {
		b := pad[i%2]
		put(int(b), 8)
	}
	return out
}

func bitsToCodewords(bits []byte) []byte {
	out := make([]byte, len(bits)/8)
	for i := range out {
		var v byte
		for j := 0; j < 8; j++ {
			v = v<<1 | bits[i*8+j]
		}
		out[i] = v
	}
	return out
}

/* ---------------- Reed-Solomon ---------------- */

var gfExp [512]byte
var gfLog [256]int

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = i
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[gfLog[a]+gfLog[b]]
}

// rsGen 生成 degree 次的生成多项式（省略最高项系数 1）。
func rsGen(degree int) []byte {
	poly := make([]byte, degree)
	poly[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := 0; j < degree; j++ {
			poly[j] = gfMul(poly[j], root)
			if j+1 < degree {
				poly[j] ^= poly[j+1]
			}
		}
		root = gfMul(root, 2)
	}
	return poly
}

// rsEC 计算 data 的 ecLen 个纠错码字。
func rsEC(data []byte, ecLen int) []byte {
	gen := rsGen(ecLen)
	res := make([]byte, ecLen)
	for _, b := range data {
		factor := b ^ res[0]
		copy(res, res[1:])
		res[ecLen-1] = 0
		for i := 0; i < ecLen; i++ {
			res[i] ^= gfMul(gen[i], factor)
		}
	}
	return res
}

// addEC 按块分组、算纠错、再交错排列（标准要求数据与纠错都交错）。
func addEC(data []byte, sp blockSpec, ver int) []byte {
	var blocks [][]byte
	var ecs [][]byte
	pos := 0
	for g, cnt := range sp.blocks {
		for i := 0; i < cnt; i++ {
			dl := sp.dataLens[g]
			blk := data[pos : pos+dl]
			pos += dl
			blocks = append(blocks, blk)
			ecs = append(ecs, rsEC(blk, sp.ec))
		}
	}
	var out []byte
	maxData := 0
	for _, b := range blocks {
		if len(b) > maxData {
			maxData = len(b)
		}
	}
	for i := 0; i < maxData; i++ {
		for _, b := range blocks {
			if i < len(b) {
				out = append(out, b[i])
			}
		}
	}
	for i := 0; i < sp.ec; i++ {
		for _, e := range ecs {
			out = append(out, e[i])
		}
	}
	return out
}

/* ---------------- 矩阵绘制 ---------------- */

func newMatrix(ver int) [][]bool {
	size := ver*4 + 17
	m := make([][]bool, size)
	for i := range m {
		m[i] = make([]bool, size)
	}
	return m
}

func cloneMatrix(m [][]bool) [][]bool {
	out := make([][]bool, len(m))
	for i := range m {
		out[i] = append([]bool(nil), m[i]...)
	}
	return out
}

// reserved 记录功能图案占用的位置，放数据时跳过。
func reserved(ver int) [][]bool {
	size := ver*4 + 17
	r := make([][]bool, size)
	for i := range r {
		r[i] = make([]bool, size)
	}
	mark := func(x, y int) {
		if x >= 0 && y >= 0 && x < size && y < size {
			r[y][x] = true
		}
	}
	// 三个定位图案及其分隔符占 9x9 区域（含周边白边）。
	for _, c := range [][2]int{{0, 0}, {size - 7, 0}, {0, size - 7}} {
		for dy := -1; dy <= 7; dy++ {
			for dx := -1; dx <= 7; dx++ {
				mark(c[0]+dx, c[1]+dy)
			}
		}
	}
	// 定时图案
	for i := 0; i < size; i++ {
		mark(6, i)
		mark(i, 6)
	}
	// 对齐图案
	if ap := alignPos[ver]; ap != nil {
		for _, cy := range ap {
			for _, cx := range ap {
				// 与定位图案重叠的位置不画
				if (cx == 6 && cy == 6) || (cx == 6 && cy == size-7) || (cx == size-7 && cy == 6) {
					continue
				}
				for dy := -2; dy <= 2; dy++ {
					for dx := -2; dx <= 2; dx++ {
						mark(cx+dx, cy+dy)
					}
				}
			}
		}
	}
	// 格式信息区
	for i := 0; i < 9; i++ {
		mark(8, i)
		mark(i, 8)
	}
	for i := 0; i < 8; i++ {
		mark(size-1-i, 8)
		mark(8, size-1-i)
	}
	mark(8, size-8) // 固定的深色模块
	// 版本信息区
	if ver >= 7 {
		for i := 0; i < 6; i++ {
			for j := 0; j < 3; j++ {
				mark(size-11+j, i)
				mark(i, size-11+j)
			}
		}
	}
	return r
}

// drawFinder 画 7x7 定位图案。
func drawFinder(m [][]bool, x0, y0 int) {
	for dy := 0; dy < 7; dy++ {
		for dx := 0; dx < 7; dx++ {
			on := dx == 0 || dx == 6 || dy == 0 || dy == 6 ||
				(dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4)
			m[y0+dy][x0+dx] = on
		}
	}
}

func placeFunctionPatterns(m [][]bool, ver int) {
	size := ver*4 + 17
	drawFinder(m, 0, 0)
	drawFinder(m, size-7, 0)
	drawFinder(m, 0, size-7)

	// 定时图案：交替深浅。
	for i := 8; i < size-8; i++ {
		on := i%2 == 0
		m[6][i] = on
		m[i][6] = on
	}

	// 对齐图案：5x5，中心深色。
	if ap := alignPos[ver]; ap != nil {
		for _, cy := range ap {
			for _, cx := range ap {
				if (cx == 6 && cy == 6) || (cx == 6 && cy == size-7) || (cx == size-7 && cy == 6) {
					continue
				}
				for dy := -2; dy <= 2; dy++ {
					for dx := -2; dx <= 2; dx++ {
						on := abs(dx) == 2 || abs(dy) == 2 || (dx == 0 && dy == 0)
						m[cy+dy][cx+dx] = on
					}
				}
			}
		}
	}

	// 固定的深色模块
	m[size-8][8] = true
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// placeData 按标准 zigzag 顺序填数据位。
func placeData(m [][]bool, data []byte, ver int) {
	size := ver*4 + 17
	res := reserved(ver)
	bit := 0
	total := len(data) * 8
	upward := true
	for right := size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5 // 跳过定时列
		}
		for i := 0; i < size; i++ {
			y := i
			if upward {
				y = size - 1 - i
			}
			for k := 0; k < 2; k++ {
				x := right - k
				if res[y][x] {
					continue
				}
				on := false
				if bit < total {
					on = (data[bit>>3]>>(7-uint(bit&7)))&1 == 1
				}
				m[y][x] = on
				bit++
			}
		}
		upward = !upward
	}
}

// maskFn 8 种掩码条件。
func maskFn(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (y/2+x/3)%2 == 0
	case 5:
		return (x*y)%2+(x*y)%3 == 0
	case 6:
		return ((x*y)%2+(x*y)%3)%2 == 0
	default:
		return ((x+y)%2+(x*y)%3)%2 == 0
	}
}

// applyMask 只对非功能区取反（功能区需按 reserved 判断）。
func applyMask(m [][]bool, mask int) {
	ver := (len(m) - 17) / 4
	res := reserved(ver)
	for y := 0; y < len(m); y++ {
		for x := 0; x < len(m); x++ {
			if res[y][x] {
				continue
			}
			if maskFn(mask, x, y) {
				m[y][x] = !m[y][x]
			}
		}
	}
}

// drawFormat 写入 15 位格式信息（含 BCH 校验与固定掩码 0x5412）。
func drawFormat(m [][]bool, level Level, mask int) {
	data := level.bits()<<3 | mask
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := (data<<10 | rem) ^ 0x5412

	size := len(m)
	get := func(i int) bool { return (bits>>uint(i))&1 == 1 }

	// 左上角：位 0~5 在 (8,0..5)，位 6 在 (8,7)，位 7 在 (8,8)，位 8 在 (7,8)，
	// 位 9~14 在 (14-i, 8)。
	for i := 0; i <= 5; i++ {
		m[i][8] = get(i)
	}
	m[7][8] = get(6)
	m[8][8] = get(7)
	m[8][7] = get(8)
	for i := 9; i <= 14; i++ {
		m[8][14-i] = get(i)
	}
	// 右上角：位 0~7 在 (size-1-x, 8)
	for i := 0; i <= 7; i++ {
		m[8][size-1-i] = get(i)
	}
	// 左下角：位 8~14 在 (8, size-15+i)
	for i := 8; i <= 14; i++ {
		m[size-15+i][8] = get(i)
	}
	m[size-8][8] = true
}

// drawVersion 写入 18 位版本信息（版本 7 及以上）。
func drawVersion(m [][]bool, ver int) {
	rem := ver
	for i := 0; i < 12; i++ {
		rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
	}
	bits := ver<<12 | rem
	size := len(m)
	for i := 0; i < 18; i++ {
		on := (bits>>uint(i))&1 == 1
		x := i / 3
		y := size - 11 + i%3
		m[y][x] = on
		m[x][y] = on
	}
}

// penalty 按规范的四个规则给矩阵打分，分数越低越好。
func penalty(m [][]bool) int {
	size := len(m)
	score := 0

	// 规则 1：同色连续 5 个以上
	for y := 0; y < size; y++ {
		run := 1
		for x := 1; x < size; x++ {
			if m[y][x] == m[y][x-1] {
				run++
			} else {
				if run >= 5 {
					score += 3 + run - 5
				}
				run = 1
			}
		}
		if run >= 5 {
			score += 3 + run - 5
		}
	}
	for x := 0; x < size; x++ {
		run := 1
		for y := 1; y < size; y++ {
			if m[y][x] == m[y-1][x] {
				run++
			} else {
				if run >= 5 {
					score += 3 + run - 5
				}
				run = 1
			}
		}
		if run >= 5 {
			score += 3 + run - 5
		}
	}

	// 规则 2：2x2 同色块
	for y := 0; y < size-1; y++ {
		for x := 0; x < size-1; x++ {
			c := m[y][x]
			if c == m[y][x+1] && c == m[y+1][x] && c == m[y+1][x+1] {
				score += 3
			}
		}
	}

	// 规则 3：类似定位图案的 1:1:3:1:1 序列
	pat1 := []bool{true, false, true, true, true, false, true, false, false, false, false}
	pat2 := []bool{false, false, false, false, true, false, true, true, true, false, true}
	check := func(get func(i int) bool) {
		for i := 0; i+11 <= size; i++ {
			ok1, ok2 := true, true
			for j := 0; j < 11; j++ {
				if get(i+j) != pat1[j] {
					ok1 = false
				}
				if get(i+j) != pat2[j] {
					ok2 = false
				}
			}
			if ok1 {
				score += 40
			}
			if ok2 {
				score += 40
			}
		}
	}
	for y := 0; y < size; y++ {
		row := y
		check(func(i int) bool { return m[row][i] })
	}
	for x := 0; x < size; x++ {
		col := x
		check(func(i int) bool { return m[i][col] })
	}

	// 规则 4：深色比例偏离 50%
	dark := 0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if m[y][x] {
				dark++
			}
		}
	}
	total := size * size
	percent := dark * 100 / total
	step := abs(percent-50) / 5
	score += step * 10
	return score
}

// String 用两列字符画出矩阵，便于在终端与日志里肉眼确认。
func (m *Matrix) String() string {
	var b strings.Builder
	for y := 0; y < m.Size; y++ {
		for x := 0; x < m.Size; x++ {
			if m.Modules[y][x] {
				b.WriteString("██")
			} else {
				b.WriteString("  ")
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
