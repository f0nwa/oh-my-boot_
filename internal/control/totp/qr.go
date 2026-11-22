package totp

import (
	"errors"
	"fmt"
	"strings"
)

// Pure-Go ISO/IEC 18004 QR code encoder (Byte mode, ECC Level L, Versions 1..10)
// for offline SVG rendering without external dependencies.

type qrVersionSpec struct {
	version    int
	size       int
	totalCode  int
	ecPerBlock int
	g1Blocks   int
	g1Data     int
	g2Blocks   int
	g2Data     int
	align      []int
}

var qrVersionsL = []qrVersionSpec{
	{1, 21, 26, 7, 1, 19, 0, 0, nil},
	{2, 25, 44, 10, 1, 34, 0, 0, []int{6, 18}},
	{3, 29, 70, 15, 1, 55, 0, 0, []int{6, 22}},
	{4, 33, 100, 20, 1, 80, 0, 0, []int{6, 26}},
	{5, 37, 134, 26, 1, 108, 0, 0, []int{6, 30}},
	{6, 41, 172, 18, 2, 68, 0, 0, []int{6, 34}},
	{7, 45, 196, 20, 2, 78, 0, 0, []int{6, 22, 38}},
	{8, 49, 242, 24, 2, 97, 0, 0, []int{6, 24, 42}},
	{9, 53, 292, 30, 2, 116, 0, 0, []int{6, 26, 46}},
	{10, 57, 346, 18, 2, 68, 2, 69, []int{6, 28, 50}},
}

// 18-bit Golay-coded version info strings for versions 7..10.
var qrVersionInfo = map[int]uint32{
	7:  0x07C94,
	8:  0x085BC,
	9:  0x09A99,
	10: 0x0A4D3,
}

// 15-bit BCH-coded format strings for ECC Level L (01) and masks 0..7.
var qrFormatInfoL = [8]uint16{
	0x77C4, 0x72F3, 0x7DAA, 0x789D, 0x662F, 0x6318, 0x6C41, 0x6976,
}

var gfExp [512]byte
var gfLog [256]byte

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = byte(i)
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
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func rsGeneratorPoly(degree int) []byte {
	poly := []byte{1}
	for i := 0; i < degree; i++ {
		next := make([]byte, len(poly)+1)
		factor := gfExp[i]
		for j, coef := range poly {
			next[j] ^= coef
			next[j+1] ^= gfMul(coef, factor)
		}
		poly = next
	}
	return poly
}

func rsEncode(data []byte, ecLen int) []byte {
	gen := rsGeneratorPoly(ecLen)
	rem := make([]byte, ecLen)
	for _, b := range data {
		factor := b ^ rem[0]
		copy(rem, rem[1:])
		rem[ecLen-1] = 0
		if factor != 0 {
			for j := 0; j < ecLen; j++ {
				rem[j] ^= gfMul(gen[j+1], factor)
			}
		}
	}
	return rem
}

type bitBuf struct {
	bits []byte
}

func (b *bitBuf) appendBits(val uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		b.bits = append(b.bits, byte((val>>uint(i))&1))
	}
}

func (b *bitBuf) bytes() []byte {
	out := make([]byte, (len(b.bits)+7)/8)
	for i, bit := range b.bits {
		if bit != 0 {
			out[i/8] |= 1 << uint(7-(i%8))
		}
	}
	return out
}

func selectQRVersion(dataLen int) (qrVersionSpec, error) {
	for _, v := range qrVersionsL {
		capBytes := v.g1Blocks*v.g1Data + v.g2Blocks*v.g2Data
		headerBits := 4 + 8 // mode(4) + len(8) for v1..v9
		if v.version >= 10 {
			headerBits = 4 + 16
		}
		if headerBits+dataLen*8 <= capBytes*8 {
			return v, nil
		}
	}
	return qrVersionSpec{}, errors.New("qr payload exceeds supported version 10 capacity")
}

// EncodeQR encodes data into a square boolean matrix (true = dark module).
func EncodeQR(data []byte) ([][]bool, error) {
	spec, err := selectQRVersion(len(data))
	if err != nil {
		return nil, err
	}
	totalData := spec.g1Blocks*spec.g1Data + spec.g2Blocks*spec.g2Data

	var bb bitBuf
	bb.appendBits(0b0100, 4) // Byte mode
	if spec.version >= 10 {
		bb.appendBits(uint32(len(data)), 16)
	} else {
		bb.appendBits(uint32(len(data)), 8)
	}
	for _, c := range data {
		bb.appendBits(uint32(c), 8)
	}
	// Terminator up to 4 zero bits.
	maxBits := totalData * 8
	term := maxBits - len(bb.bits)
	if term > 4 {
		term = 4
	}
	bb.appendBits(0, term)
	if rem := len(bb.bits) % 8; rem != 0 {
		bb.appendBits(0, 8-rem)
	}
	codewords := bb.bytes()
	padBytes := [2]byte{0xEC, 0x11}
	for i := 0; len(codewords) < totalData; i++ {
		codewords = append(codewords, padBytes[i%2])
	}

	// Split into blocks and compute RS ECC.
	var dataBlocks [][]byte
	var ecBlocks [][]byte
	offset := 0
	for i := 0; i < spec.g1Blocks; i++ {
		blk := codewords[offset : offset+spec.g1Data]
		offset += spec.g1Data
		dataBlocks = append(dataBlocks, blk)
		ecBlocks = append(ecBlocks, rsEncode(blk, spec.ecPerBlock))
	}
	for i := 0; i < spec.g2Blocks; i++ {
		blk := codewords[offset : offset+spec.g2Data]
		offset += spec.g2Data
		dataBlocks = append(dataBlocks, blk)
		ecBlocks = append(ecBlocks, rsEncode(blk, spec.ecPerBlock))
	}

	// Interleave data and EC codewords.
	finalBytes := make([]byte, 0, spec.totalCode)
	maxDataLen := spec.g1Data
	if spec.g2Data > maxDataLen {
		maxDataLen = spec.g2Data
	}
	for i := 0; i < maxDataLen; i++ {
		for _, blk := range dataBlocks {
			if i < len(blk) {
				finalBytes = append(finalBytes, blk[i])
			}
		}
	}
	for i := 0; i < spec.ecPerBlock; i++ {
		for _, ec := range ecBlocks {
			finalBytes = append(finalBytes, ec[i])
		}
	}

	// Build base matrix with function patterns.
	n := spec.size
	mod := make([][]bool, n)
	fn := make([][]bool, n)
	for y := 0; y < n; y++ {
		mod[y] = make([]bool, n)
		fn[y] = make([]bool, n)
	}
	setFn := func(x, y int, dark bool) {
		if x >= 0 && x < n && y >= 0 && y < n {
			mod[y][x] = dark
			fn[y][x] = true
		}
	}
	drawFinder := func(cx, cy int) {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				adx, ady := dx, dy
				if adx < 0 {
					adx = -adx
				}
				if ady < 0 {
					ady = -ady
				}
				dist := adx
				if ady > dist {
					dist = ady
				}
				dark := dist != 2 && dist != 4
				setFn(cx+dx, cy+dy, dark)
			}
		}
	}
	drawFinder(3, 3)
	drawFinder(n-4, 3)
	drawFinder(3, n-4)

	// Timing patterns.
	for i := 8; i < n-8; i++ {
		setFn(i, 6, i%2 == 0)
		setFn(6, i, i%2 == 0)
	}

	// Alignment patterns.
	for _, cy := range spec.align {
		for _, cx := range spec.align {
			if fn[cy][cx] {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					adx, ady := dx, dy
					if adx < 0 {
						adx = -adx
					}
					if ady < 0 {
						ady = -ady
					}
					dist := adx
					if ady > dist {
						dist = ady
					}
					setFn(cx+dx, cy+dy, dist != 1)
				}
			}
		}
	}

	// Reserve format info areas and dark module.
	for i := 0; i <= 8; i++ {
		if i != 6 {
			setFn(8, i, false)
			setFn(i, 8, false)
		}
	}
	for i := 0; i < 8; i++ {
		setFn(n-1-i, 8, false)
	}
	for i := 0; i < 7; i++ {
		setFn(8, n-1-i, false)
	}
	setFn(8, n-8, true) // Dark module

	// Version info for v7..v10.
	if vinfo, ok := qrVersionInfo[spec.version]; ok {
		for i := 0; i < 18; i++ {
			dark := ((vinfo >> uint(i)) & 1) != 0
			a := n - 11 + (i % 3)
			b := i / 3
			setFn(a, b, dark)
			setFn(b, a, dark)
		}
	}

	// Place data bits in zig-zag columns.
	bitIdx := 0
	totalBits := len(finalBytes) * 8
	for right := n - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		upward := ((right + 1) & 2) == 0
		for vert := 0; vert < n; vert++ {
			y := vert
			if upward {
				y = n - 1 - vert
			}
			for j := 0; j < 2; j++ {
				x := right - j
				if !fn[y][x] {
					dark := false
					if bitIdx < totalBits {
						dark = ((finalBytes[bitIdx/8] >> uint(7-(bitIdx%8))) & 1) != 0
						bitIdx++
					}
					mod[y][x] = dark
				}
			}
		}
	}

	// Evaluate all 8 masks and pick the lowest penalty.
	bestScore := 1 << 30
	var best [][]bool
	for mask := 0; mask < 8; mask++ {
		cand := applyMask(mod, fn, mask)
		writeFormatInfo(cand, qrFormatInfoL[mask])
		score := penaltyScore(cand)
		if score < bestScore {
			bestScore = score
			best = cand
		}
	}
	return best, nil
}

func maskBit(mask, x, y int) bool {
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

func applyMask(mod, fn [][]bool, mask int) [][]bool {
	n := len(mod)
	out := make([][]bool, n)
	for y := 0; y < n; y++ {
		out[y] = make([]bool, n)
		for x := 0; x < n; x++ {
			v := mod[y][x]
			if !fn[y][x] && maskBit(mask, x, y) {
				v = !v
			}
			out[y][x] = v
		}
	}
	return out
}

func writeFormatInfo(m [][]bool, bits uint16) {
	n := len(m)
	for i := 0; i <= 5; i++ {
		m[8][i] = ((bits >> uint(14-i)) & 1) != 0
	}
	m[8][7] = ((bits >> 8) & 1) != 0
	m[8][8] = ((bits >> 7) & 1) != 0
	m[7][8] = ((bits >> 6) & 1) != 0
	for i := 9; i < 15; i++ {
		m[14-i][8] = ((bits >> uint(14-i)) & 1) != 0
	}
	for i := 0; i < 8; i++ {
		m[8][n-1-i] = ((bits >> uint(i)) & 1) != 0
	}
	for i := 0; i < 7; i++ {
		m[n-1-i][8] = ((bits >> uint(14-i)) & 1) != 0
	}
}

func penaltyScore(m [][]bool) int {
	n := len(m)
	score := 0
	// Rule 1: runs of 5+ same color in rows and columns.
	for y := 0; y < n; y++ {
		run := 1
		for x := 1; x < n; x++ {
			if m[y][x] == m[y][x-1] {
				run++
				if run == 5 {
					score += 3
				} else if run > 5 {
					score++
				}
			} else {
				run = 1
			}
		}
	}
	for x := 0; x < n; x++ {
		run := 1
		for y := 1; y < n; y++ {
			if m[y][x] == m[y-1][x] {
				run++
				if run == 5 {
					score += 3
				} else if run > 5 {
					score++
				}
			} else {
				run = 1
			}
		}
	}
	// Rule 2: 2x2 blocks of same color.
	for y := 0; y < n-1; y++ {
		for x := 0; x < n-1; x++ {
			c := m[y][x]
			if m[y][x+1] == c && m[y+1][x] == c && m[y+1][x+1] == c {
				score += 3
			}
		}
	}
	// Rule 4: dark/light balance.
	dark := 0
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if m[y][x] {
				dark++
			}
		}
	}
	total := n * n
	diff := dark*20 - total*10
	if diff < 0 {
		diff = -diff
	}
	score += (diff / total) * 10
	return score
}

// RenderQRSVG renders data into a self-contained SVG QR code with a 4-module quiet zone.
func RenderQRSVG(data string) (string, error) {
	matrix, err := EncodeQR([]byte(data))
	if err != nil {
		return "", err
	}
	const border = 4
	n := len(matrix)
	dim := n + border*2
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges" role="img" aria-label="QR code"><rect width="%d" height="%d" fill="#ffffff"/><path fill="#111111" d="`, dim, dim, dim, dim)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if matrix[y][x] {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+border, y+border)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String(), nil
}
