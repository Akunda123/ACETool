// Package qr 是二维码编码器（byte 模式、纠错等级 H、版本 1–40 自动选）。
//
// 为什么自己写：参考实现是 SV 脚本 `二维码.js` 里的 Arase 编码器（MIT），那个脚本只能跑在 SV 里；
// 这里按同样的算法与**同一套掩码评分**移植，保证同一文本得到同一张图（与旧图可对照）。
//
// 已用交接文档的实测值验过（见 qr_test.go）：
//
//	"1"                   → v1 21×21 · 224 个暗模块 · mask 4 · 格式信息 0x762
//	"https://example.com" → v3 29×29
package qr

import "fmt"

// Code 是编好的二维码。
type Code struct {
	Version int      // 1..40
	Size    int      // 17+4*Version
	Mask    int      // 0..7（按参考实现的 lost-point 评分选出来）
	Module  [][]bool // Module[row][col]：true = 暗模块（= 要放一个音符）
}

// Encode 把文本编成二维码（byte 模式、纠错 H、版本自动选）。
func Encode(text string) (*Code, error) {
	data := []byte(text)
	v := 0
	for cand := 1; cand <= 40; cand++ {
		if 4+lenBits(cand)+8*len(data) <= dataCapacity(cand)*8 {
			v = cand
			break
		}
	}
	if v == 0 {
		return nil, fmt.Errorf("文本太长：%d 字节，H 级最大版本也放不下", len(data))
	}
	return build(v, interleave(dataCodewords(data, v), v))
}

func lenBits(version int) int {
	if version < 10 {
		return 8
	}
	return 16
}

// blocks 把 rsRawH[v-1] 展开成块列表（[count,total,data] 三元组）。
func blocks(version int) []struct{ total, data int } {
	raw := rsRawH[version-1]
	var out []struct{ total, data int }
	for i := 0; i+2 < len(raw); i += 3 {
		for c := 0; c < raw[i]; c++ {
			out = append(out, struct{ total, data int }{raw[i+1], raw[i+2]})
		}
	}
	return out
}

func dataCapacity(version int) int {
	n := 0
	for _, b := range blocks(version) {
		n += b.data
	}
	return n
}

// dataCodewords 生成数据码字（模式/长度/填充）。
func dataCodewords(data []byte, version int) []byte {
	var bits []bool
	put := func(v, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, (v>>uint(i))&1 == 1)
		}
	}
	put(4, 4) // byte 模式
	put(len(data), lenBits(version))
	for _, b := range data {
		put(int(b), 8)
	}
	capBits := dataCapacity(version) * 8
	for i := 0; i < 4 && len(bits) < capBits; i++ {
		bits = append(bits, false)
	}
	for len(bits)%8 != 0 {
		bits = append(bits, false)
	}
	out := make([]byte, 0, dataCapacity(version))
	for i := 0; i < len(bits); i += 8 {
		var b byte
		for j := 0; j < 8; j++ {
			if bits[i+j] {
				b |= 1 << uint(7-j)
			}
		}
		out = append(out, b)
	}
	pad := []byte{0xEC, 0x11}
	for k := 0; len(out) < dataCapacity(version); k++ {
		out = append(out, pad[k%2])
	}
	return out
}

// ---------- GF(256) 与 RS 纠错 ----------

var (
	gfExp [512]byte
	gfLog [256]int
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = i
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
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

func genPoly(ecLen int) []byte {
	g := []byte{1}
	for i := 0; i < ecLen; i++ {
		next := make([]byte, len(g)+1)
		for j, c := range g {
			next[j] ^= c
			next[j+1] ^= gfMul(c, gfExp[i])
		}
		g = next
	}
	return g
}

func rsEC(data []byte, ecLen int) []byte {
	g := genPoly(ecLen)
	rem := make([]byte, ecLen)
	for _, b := range data {
		f := b ^ rem[0]
		copy(rem, rem[1:])
		rem[ecLen-1] = 0
		if f != 0 {
			for j := 0; j < ecLen; j++ {
				rem[j] ^= gfMul(g[j+1], f)
			}
		}
	}
	return rem
}

// interleave 按块算纠错、再交织成最终码字序列。
func interleave(cw []byte, version int) []byte {
	bs := blocks(version)
	var datas, ecs [][]byte
	off := 0
	for _, b := range bs {
		d := cw[off : off+b.data]
		off += b.data
		datas = append(datas, d)
		ecs = append(ecs, rsEC(d, b.total-b.data))
	}
	var out []byte
	maxD, maxE := 0, 0
	for i, d := range datas {
		if len(d) > maxD {
			maxD = len(d)
		}
		if len(ecs[i]) > maxE {
			maxE = len(ecs[i])
		}
	}
	for i := 0; i < maxD; i++ {
		for _, d := range datas {
			if i < len(d) {
				out = append(out, d[i])
			}
		}
	}
	for i := 0; i < maxE; i++ {
		for _, e := range ecs {
			if i < len(e) {
				out = append(out, e[i])
			}
		}
	}
	return out
}

// ---------- 矩阵 ----------

func build(version int, cw []byte) (*Code, error) {
	size := 17 + 4*version
	m := make([][]bool, size)
	fixed := make([][]bool, size)
	for i := range m {
		m[i] = make([]bool, size)
		fixed[i] = make([]bool, size)
	}
	set := func(r, c int, dark bool) {
		if r < 0 || c < 0 || r >= size || c >= size {
			return
		}
		m[r][c] = dark
		fixed[r][c] = true
	}
	// 三个定位图案（含分隔白边）
	for _, rc := range [][2]int{{0, 0}, {0, size - 7}, {size - 7, 0}} {
		for r := -1; r <= 7; r++ {
			for c := -1; c <= 7; c++ {
				dark := (r >= 0 && r <= 6 && (c == 0 || c == 6)) ||
					(c >= 0 && c <= 6 && (r == 0 || r == 6)) ||
					(r >= 2 && r <= 4 && c >= 2 && c <= 4)
				set(rc[0]+r, rc[1]+c, dark)
			}
		}
	}
	// 时序行/列
	for i := 8; i < size-8; i++ {
		set(6, i, i%2 == 0)
		set(i, 6, i%2 == 0)
	}
	// 校正图案（避开三个角）
	for _, r0 := range alignPos[version-1] {
		for _, c0 := range alignPos[version-1] {
			if (r0 == 6 && c0 == 6) || (r0 == 6 && c0 == size-7) || (r0 == size-7 && c0 == 6) {
				continue
			}
			for dr := -2; dr <= 2; dr++ {
				for dc := -2; dc <= 2; dc++ {
					set(r0+dr, c0+dc, abs(dr) == 2 || abs(dc) == 2 || (dr == 0 && dc == 0))
				}
			}
		}
	}
	set(size-8, 8, true) // 固定暗模块
	// 格式信息那 30 个格子**先占住**（参考实现里它们非 null，数据区会跳过）
	for i := 0; i <= 5; i++ {
		fixed[i][8] = true
	}
	fixed[7][8], fixed[8][8], fixed[8][7] = true, true, true
	for i := 8; i < 15; i++ {
		if r := size - 15 + i; r >= 0 && r < size {
			fixed[r][8] = true
		}
	}
	for i := 0; i < 8; i++ {
		fixed[8][size-1-i] = true
	}
	for i := 9; i < 15; i++ {
		fixed[8][14-i] = true
	}

	// 数据区（右下起，两列一组，蛇形）
	i := 0
	for col := size - 1; col > 0; col -= 2 {
		if col == 6 {
			col--
		}
		for vert := 0; vert < size; vert++ {
			for k := 0; k < 2; k++ {
				c := col - k
				r := vert
				if (col+1)&2 == 0 {
					r = size - 1 - vert
				}
				if fixed[r][c] {
					continue
				}
				if i < len(cw)*8 {
					m[r][c] = cw[i/8]>>uint(7-i%8)&1 == 1
				}
				i++
			}
		}
	}
	// 选掩码：参考实现的评分（含格式信息，跟它一致）
	best, bestScore := 0, -1.0
	base := clone(m)
	for mask := 0; mask < 8; mask++ {
		t := clone(base)
		applyMask(t, fixed, mask)
		writeFormat(t, mask)
		if sc := penalty(t, size); bestScore < 0 || sc < bestScore {
			best, bestScore = mask, sc
		}
	}
	applyMask(base, fixed, best)
	writeFormat(base, best)
	return &Code{Version: version, Size: size, Mask: best, Module: base}, nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func clone(m [][]bool) [][]bool {
	out := make([][]bool, len(m))
	for i := range m {
		out[i] = append([]bool(nil), m[i]...)
	}
	return out
}

func maskHit(mask, i, j int) bool {
	switch mask {
	case 0:
		return (i+j)%2 == 0
	case 1:
		return i%2 == 0
	case 2:
		return j%3 == 0
	case 3:
		return (i+j)%3 == 0
	case 4:
		return (i/2+j/3)%2 == 0
	case 5:
		return (i*j)%2+(i*j)%3 == 0
	case 6:
		return ((i*j)%2+(i*j)%3)%2 == 0
	case 7:
		return ((i*j)%3+(i+j)%2)%2 == 0
	}
	return false
}

func applyMask(m, fixed [][]bool, mask int) {
	for r := range m {
		for c := range m[r] {
			if !fixed[r][c] && maskHit(mask, r, c) {
				m[r][c] = !m[r][c]
			}
		}
	}
}

// writeFormat 按**参考实现的原位摆放**写 15 位格式信息（BCH 已含 0x5412 掩码）。
// ⚠️ 注意它是"第 i 位写到第 i 个位置"（LSB 起），且竖直那一路画在**左下**（不是左上）。
func writeFormat(m [][]bool, mask int) {
	size := len(m)
	const hBits = 2 // H = 10
	data := hBits<<3 | mask
	bits := bchBits(data)
	at := func(i int) bool { return bits>>uint(i)&1 == 1 }
	for i := 0; i < 15; i++ {
		if i < 6 {
			m[i][8] = at(i)
		} else if i < 8 {
			m[i+1][8] = at(i)
		} else {
			m[size-15+i][8] = at(i)
		}
	}
	for i := 0; i < 15; i++ {
		if i < 8 {
			m[8][size-1-i] = at(i)
		} else if i < 9 {
			m[8][15-i] = at(i) // i==8 ⇒ 7
		} else {
			m[8][15-1-i] = at(i)
		}
	}
	m[size-8][8] = true
}

// bchBits 算 15 位格式信息（BCH(15,5) + 0x5412 掩码）。
func bchBits(data int) int {
	bch := data << 10
	for bchDigit(bch) >= bchDigit(0x537) {
		bch ^= 0x537 << uint(bchDigit(bch)-bchDigit(0x537))
	}
	return (data<<10 | bch) ^ 0x5412
}

// FormatBits 返回这一张图的 15 位格式信息（测试用；含 0x5412 掩码后的值）。
func (c *Code) FormatBits() int { return bchBits(2<<3 | c.Mask) }

func bchDigit(v int) int {
	d := 0
	for v != 0 {
		d++
		v >>= 1
	}
	return d
}

// penalty 复刻参考脚本的 lost-point（与规范略有差别，但必须一致才能选出同一个 mask）。
func penalty(m [][]bool, size int) float64 {
	score := 0
	for row := 0; row < size; row++ {
		for col := 0; col < size; col++ {
			dark := m[row][col]
			same := 0
			for r := -1; r <= 1; r++ {
				for c := -1; c <= 1; c++ {
					if r == 0 && c == 0 {
						continue
					}
					rr, cc := row+r, col+c
					if rr < 0 || cc < 0 || rr >= size || cc >= size {
						continue
					}
					if m[rr][cc] == dark {
						same++
					}
				}
			}
			if same > 5 {
				score += 3 + same - 5
			}
		}
	}
	for row := 0; row < size-1; row++ {
		for col := 0; col < size-1; col++ {
			n := 0
			for _, rc := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
				if m[row+rc[0]][col+rc[1]] {
					n++
				}
			}
			if n == 0 || n == 4 {
				score += 3
			}
		}
	}
	for row := 0; row < size; row++ {
		for col := 0; col < size-6; col++ {
			if m[row][col] && !m[row][col+1] && m[row][col+2] && m[row][col+3] && m[row][col+4] && !m[row][col+5] && m[row][col+6] {
				score += 40
			}
		}
	}
	for col := 0; col < size; col++ {
		for row := 0; row < size-6; row++ {
			if m[row][col] && !m[row+1][col] && m[row+2][col] && m[row+3][col] && m[row+4][col] && !m[row+5][col] && m[row+6][col] {
				score += 40
			}
		}
	}
	dark := 0
	for row := 0; row < size; row++ {
		for col := 0; col < size; col++ {
			if m[row][col] {
				dark++
			}
		}
	}
	ratio := float64(abs(100*dark/size/size-50)) / 5
	return float64(score) + ratio*10
}

// DarkCount 数暗模块（测试与报告用）。
func (c *Code) DarkCount() int {
	n := 0
	for _, row := range c.Module {
		for _, d := range row {
			if d {
				n++
			}
		}
	}
	return n
}
