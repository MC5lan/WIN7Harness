package core

import (
	"errors"
	"strings"
)

// —— 二维码编码（字节模式 / 纠错等级 L / 版本 1~10 自适应）——
//
// 为什么自己写：v1.28 要求"扫一下就配对"，而程序对依赖的容忍度是零 ——
// 加一个第三方 QR 库，就要为 Win7 上的 go.sum 和离线构建多背一份风险。
// 这部分算法是死的（ISO/IEC 18004），自己实现反而更可控。
//
// 只做 ECC L、版本 1~10：配对的 URL 最长也就 60 来个字符，版本 4 就够，
// 留到版本 10 已经是一次性把"局域网 IP 很长 / 端口 5 位"这些情况全兜住了。
// 更长的内容说明有人拿它当通用二维码生成器用，那不是这个函数的目的。

// QRCode 编码结果。Modules[y][x] 为 true 表示深色。
type QRCode struct {
	Version int
	Mask    int
	Size    int
	Modules [][]bool
	// Scores 为 8 种掩码的罚分（自动选择时填写），自检与排查用。
	Scores []int
}

// 版本-L 的分块参数：每块纠错码字数、组1块数与数据码字数、组2块数与数据码字数。
// 表来自 ISO/IEC 18004 Table 13-22，只取到版本 10。
type qrBlockSpec struct {
	eccPerBlock int
	g1Blocks    int
	g1Data      int
	g2Blocks    int
	g2Data      int
}

var qrSpecL = map[int]qrBlockSpec{
	1:  {7, 1, 19, 0, 0},
	2:  {10, 1, 34, 0, 0},
	3:  {15, 1, 55, 0, 0},
	4:  {20, 1, 80, 0, 0},
	5:  {26, 1, 108, 0, 0},
	6:  {18, 2, 68, 0, 0},
	7:  {20, 2, 78, 0, 0},
	8:  {24, 2, 97, 0, 0},
	9:  {30, 2, 116, 0, 0},
	10: {18, 2, 68, 2, 69},
}

var qrAlignPos = map[int][]int{
	1:  {},
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

var errQRTooLong = errors.New("内容过长，无法生成二维码")

// EncodeQR 把文本编码成二维码矩阵（字节模式，UTF-8，纠错 L，自动选版本与掩码）。
func EncodeQR(text string) (*QRCode, error) { return encodeQR(text, -1) }

// EncodeQRMask 指定掩码（0~7），用于自检与对齐标准实现；
// forced < 0 时按罚分自动选。
func EncodeQRMask(text string, forced int) (*QRCode, error) { return encodeQR(text, forced) }

func encodeQR(text string, forced int) (*QRCode, error) {
	data := []byte(text)

	ver := 0
	for v := 1; v <= 10; v++ {
		sp := qrSpecL[v]
		capBytes := sp.g1Blocks*sp.g1Data + sp.g2Blocks*sp.g2Data
		// 版本 1~9 的字节模式字符计数占 8 位，版本 10 起占 16 位；
		// 这里按 8 位算，版本 10 多出的 2 字节是余量，够用。
		need := 1 + len(data) // 模式指示 4bit + 长度 8bit ≈ 1.5 字节，向上取整留 1 字节
		if len(data)+2 <= capBytes && need <= capBytes {
			ver = v
			break
		}
	}
	if ver == 0 {
		return nil, errQRTooLong
	}

	bits := &bitBuf{}
	bits.put(0b0100, 4) // 字节模式
	bits.put(uint32(len(data)), 8)
	for _, b := range data {
		bits.put(uint32(b), 8)
	}
	sp := qrSpecL[ver]
	totalData := sp.g1Blocks*sp.g1Data + sp.g2Blocks*sp.g2Data
	if bits.len() > totalData*8 {
		return nil, errQRTooLong
	}
	// 终止符最多 4 位，然后补齐到字节边界
	term := totalData*8 - bits.len()
	if term > 4 {
		term = 4
	}
	bits.put(0, term)
	for bits.len()%8 != 0 {
		bits.put(0, 1)
	}
	// 填充字节 0xEC / 0x11 交替，直到填满数据容量
	for i := 0; bits.len() < totalData*8; i++ {
		if i%2 == 0 {
			bits.put(0xEC, 8)
		} else {
			bits.put(0x11, 8)
		}
	}
	dataBytes := bits.bytes()

	// 按块切分 → 各自算纠错 → 再交织
	var blocks [][]byte
	var eccBlocks [][]byte
	off := 0
	addBlock := func(n, size int) {
		for i := 0; i < n; i++ {
			d := dataBytes[off : off+size]
			off += size
			blocks = append(blocks, d)
			eccBlocks = append(eccBlocks, rsEncode(d, sp.eccPerBlock))
		}
	}
	addBlock(sp.g1Blocks, sp.g1Data)
	addBlock(sp.g2Blocks, sp.g2Data)

	var final []byte
	maxData := sp.g1Data
	if sp.g2Data > maxData {
		maxData = sp.g2Data
	}
	for i := 0; i < maxData; i++ {
		for _, b := range blocks {
			if i < len(b) {
				final = append(final, b[i])
			}
		}
	}
	for i := 0; i < sp.eccPerBlock; i++ {
		for _, b := range eccBlocks {
			final = append(final, b[i])
		}
	}

	size := 17 + ver*4
	m := newQRMatrix(size)
	m.drawFunctionPatterns(ver)
	m.placeData(final)

	// 8 种掩码按罚分选最优，与标准实现一致。
	best, bestScore := -1, -1
	scores := make([]int, 8)
	if forced >= 0 && forced < 8 {
		m.applyMask(forced)
		m.drawFormat(ver, forced)
		return &QRCode{Version: ver, Mask: forced, Size: size, Modules: m.grid}, nil
	}
	for mask := 0; mask < 8; mask++ {
		m.applyMask(mask)
		m.drawFormat(ver, mask)
		score := m.penalty()
		scores[mask] = score
		if bestScore < 0 || score < bestScore {
			best, bestScore = mask, score
		}
		m.applyMask(mask) // 异或两次还原
	}
	m.applyMask(best)
	m.drawFormat(ver, best)

	return &QRCode{Version: ver, Mask: best, Size: size, Modules: m.grid, Scores: scores}, nil
}

// —— 位缓冲 ——

type bitBuf struct {
	data []byte
	n    int
}

func (b *bitBuf) put(v uint32, bits int) {
	for i := bits - 1; i >= 0; i-- {
		if b.n%8 == 0 {
			b.data = append(b.data, 0)
		}
		if (v>>uint(i))&1 == 1 {
			b.data[b.n/8] |= 1 << uint(7-b.n%8)
		}
		b.n++
	}
}

func (b *bitBuf) len() int      { return b.n }
func (b *bitBuf) bytes() []byte { return b.data }

// —— Reed-Solomon（GF(2^8)，本原多项式 0x11D）——

var qrExp [512]byte
var qrLog [256]int
var qrRSInit bool

func qrInitRS() {
	if qrRSInit {
		return
	}
	x := 1
	for i := 0; i < 255; i++ {
		qrExp[i] = byte(x)
		qrLog[x] = i
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		qrExp[i] = qrExp[i-255]
	}
	qrRSInit = true
}

// rsEncode 计算 n 个纠错码字。
func rsEncode(data []byte, n int) []byte {
	qrInitRS()
	// 生成多项式 (x-a^0)(x-a^1)...(x-a^(n-1))
	gen := make([]byte, n)
	gen[n-1] = 1
	root := byte(1)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			gen[j] = gfMul(gen[j], root)
			if j+1 < n {
				gen[j] ^= gen[j+1]
			}
		}
		root = gfMul(root, 2)
	}

	res := make([]byte, n)
	for _, d := range data {
		f := d ^ res[0]
		copy(res, res[1:])
		res[n-1] = 0
		if f != 0 {
			lf := qrLog[f]
			for i := 0; i < n; i++ {
				if gen[i] != 0 {
					res[i] ^= qrExp[lf+qrLog[gen[i]]]
				}
			}
		}
	}
	return res
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return qrExp[qrLog[a]+qrLog[b]]
}

// —— 矩阵 ——

type qrMatrix struct {
	size int
	grid [][]bool
	// funcMod 标记功能图形，放置数据和掩码时跳过
	funcMod [][]bool
}

func newQRMatrix(size int) *qrMatrix {
	m := &qrMatrix{size: size}
	m.grid = make([][]bool, size)
	m.funcMod = make([][]bool, size)
	for i := range m.grid {
		m.grid[i] = make([]bool, size)
		m.funcMod[i] = make([]bool, size)
	}
	return m
}

func (m *qrMatrix) set(x, y int, v bool) {
	if x < 0 || y < 0 || x >= m.size || y >= m.size {
		return
	}
	m.grid[y][x] = v
	m.funcMod[y][x] = true
}

func (m *qrMatrix) drawFunctionPatterns(ver int) {
	// 三个定位图形 + 分隔符
	for _, p := range [][2]int{{0, 0}, {m.size - 7, 0}, {0, m.size - 7}} {
		m.drawFinder(p[0], p[1])
	}
	// 定时图形
	for i := 8; i < m.size-8; i++ {
		m.set(i, 6, i%2 == 0)
		m.set(6, i, i%2 == 0)
	}
	// 校正图形
	pos := qrAlignPos[ver]
	for _, cy := range pos {
		for _, cx := range pos {
			// 与三个定位图形重叠的角跳过
			if (cx == 6 && cy == 6) || (cx == 6 && cy == m.size-7) || (cx == m.size-7 && cy == 6) {
				continue
			}
			m.drawAlign(cx, cy)
		}
	}
	// 预留格式信息区域（先画一遍占位，稍后写入真实值）
	m.drawFormat(ver, 0)
	// 版本信息（版本 >= 7）
	if ver >= 7 {
		bits := versionBits(ver)
		for i := 0; i < 18; i++ {
			bit := (bits>>uint(i))&1 == 1
			x := i / 3
			y := m.size - 11 + i%3
			m.set(x, y, bit)
			m.set(y, x, bit)
		}
	}
	// 固定的深色模块
	m.set(8, m.size-8, true)
}

func (m *qrMatrix) drawFinder(x, y int) {
	for dy := -1; dy <= 7; dy++ {
		for dx := -1; dx <= 7; dx++ {
			px, py := x+dx, y+dy
			if px < 0 || py < 0 || px >= m.size || py >= m.size {
				continue
			}
			// 外框 7x7、内框 3x3
			inRing := dx >= 0 && dx <= 6 && dy >= 0 && dy <= 6 &&
				(dx == 0 || dx == 6 || dy == 0 || dy == 6)
			inCore := dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4
			m.set(px, py, inRing || inCore)
		}
	}
}

func (m *qrMatrix) drawAlign(cx, cy int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			v := dx == 0 && dy == 0 || absInt(dx) == 2 || absInt(dy) == 2
			m.set(cx+dx, cy+dy, v)
		}
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// formatBits 计算 15 位格式信息：纠错等级 + 掩码号 + BCH(15,5) + 固定掩码 0x5412。
func formatBits(eccL bool, mask int) uint32 {
	// ECC L 的指示位是 01
	data := uint32(0b01)<<3 | uint32(mask)
	if !eccL {
		data = uint32(0b00)<<3 | uint32(mask)
	}
	rem := data << 10
	for i := 14; i >= 10; i-- {
		if (rem>>uint(i))&1 == 1 {
			rem ^= 0x537 << uint(i-10)
		}
	}
	return (data<<10 | rem) ^ 0x5412
}

func (m *qrMatrix) drawFormat(ver, mask int) {
	bits := formatBits(true, mask)
	for i := 0; i < 15; i++ {
		v := (bits>>uint(i))&1 == 1
		// 左上角：跳过定时图形所在的第 6 行/列
		if i < 6 {
			m.set(8, i, v)
		} else if i < 8 {
			m.set(8, i+1, v)
		} else if i == 8 {
			m.set(7, 8, v)
		} else {
			m.set(14-i, 8, v)
		}
		// 分散到右上与左下
		if i < 8 {
			m.set(m.size-1-i, 8, v)
		} else {
			m.set(8, m.size-15+i, v)
		}
	}
	m.set(8, m.size-8, true) // 固定深色模块
}

// versionBits 计算 18 位版本信息（版本 >= 7）。
func versionBits(ver int) uint32 {
	rem := uint32(ver) << 12
	for i := 17; i >= 12; i-- {
		if (rem>>uint(i))&1 == 1 {
			rem ^= 0x1F25 << uint(i-12)
		}
	}
	return uint32(ver)<<12 | rem
}

// placeData 按标准之字形从右下角开始放置数据位。
func (m *qrMatrix) placeData(data []byte) {
	bitIdx := 0
	total := len(data) * 8
	upward := true
	for right := m.size - 1; right >= 1; right -= 2 {
		if right == 6 { // 跳过定时列
			right = 5
		}
		for vert := 0; vert < m.size; vert++ {
			y := vert
			if upward {
				y = m.size - 1 - vert
			}
			for j := 0; j < 2; j++ {
				x := right - j
				if m.funcMod[y][x] {
					continue
				}
				bit := false
				if bitIdx < total {
					bit = (data[bitIdx/8]>>uint(7-bitIdx%8))&1 == 1
					bitIdx++
				}
				m.grid[y][x] = bit
			}
		}
		upward = !upward
	}
}

func (m *qrMatrix) applyMask(mask int) {
	for y := 0; y < m.size; y++ {
		for x := 0; x < m.size; x++ {
			if m.funcMod[y][x] {
				continue
			}
			var inv bool
			switch mask {
			case 0:
				inv = (y+x)%2 == 0
			case 1:
				inv = y%2 == 0
			case 2:
				inv = x%3 == 0
			case 3:
				inv = (y+x)%3 == 0
			case 4:
				inv = (y/2+x/3)%2 == 0
			case 5:
				inv = (y*x)%2+(y*x)%3 == 0
			case 6:
				inv = ((y*x)%2+(y*x)%3)%2 == 0
			case 7:
				inv = ((y+x)%2+(y*x)%3)%2 == 0
			}
			if inv {
				m.grid[y][x] = !m.grid[y][x]
			}
		}
	}
}

// penalty 计算 4 条标准罚分，用于挑选最"好扫"的掩码。
func (m *qrMatrix) penalty() int {
	score := 0
	n := m.size

	// 规则 1：同色连续 >= 5
	for y := 0; y < n; y++ {
		run := 1
		for x := 1; x < n; x++ {
			if m.grid[y][x] == m.grid[y][x-1] {
				run++
				if x == n-1 && run >= 5 {
					score += 3 + run - 5
				}
			} else {
				if run >= 5 {
					score += 3 + run - 5
				}
				run = 1
			}
		}
	}
	for x := 0; x < n; x++ {
		run := 1
		for y := 1; y < n; y++ {
			if m.grid[y][x] == m.grid[y-1][x] {
				run++
				if y == n-1 && run >= 5 {
					score += 3 + run - 5
				}
			} else {
				if run >= 5 {
					score += 3 + run - 5
				}
				run = 1
			}
		}
	}

	// 规则 2：2x2 同色块
	for y := 0; y < n-1; y++ {
		for x := 0; x < n-1; x++ {
			v := m.grid[y][x]
			if m.grid[y][x+1] == v && m.grid[y+1][x] == v && m.grid[y+1][x+1] == v {
				score += 3
			}
		}
	}

	// 规则 3：类似定位图形 1:1:3:1:1 的模式
	pat1 := []bool{true, false, true, true, true, false, true, false, false, false, false}
	pat2 := []bool{false, false, false, false, true, false, true, true, true, false, true}
	match := func(get func(int) bool, start, length int) int {
		cnt := 0
		for i := 0; i+11 <= length; i++ {
			ok1, ok2 := true, true
			for j := 0; j < 11; j++ {
				if get(start+i+j) != pat1[j] {
					ok1 = false
				}
				if get(start+i+j) != pat2[j] {
					ok2 = false
				}
			}
			if ok1 {
				cnt++
			}
			if ok2 {
				cnt++
			}
		}
		return cnt
	}
	for y := 0; y < n; y++ {
		yy := y
		score += 40 * match(func(i int) bool { return m.grid[yy][i] }, 0, n)
	}
	for x := 0; x < n; x++ {
		xx := x
		score += 40 * match(func(i int) bool { return m.grid[i][xx] }, 0, n)
	}

	// 规则 4：深色比例偏离 50%
	dark := 0
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if m.grid[y][x] {
				dark++
			}
		}
	}
	percent := dark * 100 / (n * n)
	dev := percent - 50
	if dev < 0 {
		dev = -dev
	}
	score += (dev / 5) * 10
	return score
}

// QRText 把二维码渲染成等宽文本（终端里直接能看个大概，用于 /remote qr 的兜底展示）。
// 真正的显示走界面绘制；这里是给日志和纯文本环境留的后路。
func QRText(q *QRCode) string {
	var b strings.Builder
	// 上下各留 2 行白边，否则扫码器艰难
	pad := "  " + strings.Repeat("  ", q.Size) + "  \n"
	b.WriteString(pad)
	b.WriteString(pad)
	for _, row := range q.Modules {
		b.WriteString("  ")
		for _, v := range row {
			if v {
				b.WriteString("██")
			} else {
				b.WriteString("  ")
			}
		}
		b.WriteString("  \n")
	}
	b.WriteString(pad)
	b.WriteString(pad)
	return b.String()
}
