// Package qr renders QR codes as PNG. Byte mode, error-correction level L
// (maximum payload — BIP21 URIs with an embedded BOLT11 invoice run long),
// versions 1–20.
//
// Written from scratch rather than vendored: the point-of-sale screen must
// work on an appliance with no external dependencies, and a payment QR is
// exactly the kind of thing that must not come from an unreviewed library.
package qr

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
)

// ---- per-version tables (level L) ----

var (
	totalCodewords = []int{0,
		26, 44, 70, 100, 134, 172, 196, 242, 292, 346,
		404, 466, 532, 581, 655, 733, 815, 901, 991, 1085}
	ecPerBlock = []int{0,
		7, 10, 15, 20, 26, 18, 20, 24, 30, 18,
		20, 24, 26, 30, 22, 24, 28, 30, 28, 28}
	blocks = []int{0,
		1, 1, 1, 1, 1, 2, 2, 2, 2, 4,
		4, 4, 4, 4, 6, 6, 6, 6, 7, 8}

	alignCenters = [][]int{
		{}, {}, {6, 18}, {6, 22}, {6, 26}, {6, 30}, {6, 34},
		{6, 22, 38}, {6, 24, 42}, {6, 26, 46}, {6, 28, 50},
		{6, 30, 54}, {6, 32, 58}, {6, 34, 62},
		{6, 26, 46, 66}, {6, 26, 48, 70}, {6, 26, 50, 74},
		{6, 30, 54, 78}, {6, 30, 56, 82}, {6, 30, 58, 86}, {6, 34, 62, 90},
	}
)

func dataCodewords(v int) int { return totalCodewords[v] - ecPerBlock[v]*blocks[v] }

// capacity is the number of payload bytes a version holds in byte mode.
func capacity(v int) int {
	countBits := 8
	if v >= 10 {
		countBits = 16
	}
	return dataCodewords(v) - 1 - countBits/8 // mode nibble + count field
}

// ---- GF(256) ----

var (
	expTab [512]byte
	logTab [256]byte
)

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		expTab[i] = byte(x)
		logTab[x] = byte(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d // primitive polynomial
		}
	}
	for i := 255; i < 512; i++ {
		expTab[i] = expTab[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return expTab[int(logTab[a])+int(logTab[b])]
}

// rsGenerator returns the generator polynomial for n EC codewords.
func rsGenerator(n int) []byte {
	g := []byte{1}
	for i := 0; i < n; i++ {
		next := make([]byte, len(g)+1)
		for j, c := range g {
			next[j] ^= c
			next[j+1] ^= gfMul(c, expTab[i])
		}
		g = next
	}
	return g
}

func rsEncode(data []byte, n int) []byte {
	g := rsGenerator(n)
	rem := make([]byte, n)
	for _, d := range data {
		factor := d ^ rem[0]
		copy(rem, rem[1:])
		rem[n-1] = 0
		for i, c := range g[1:] {
			rem[i] ^= gfMul(c, factor)
		}
	}
	return rem
}

// ---- bit buffer ----

type bitBuf struct {
	b   []byte
	nbi int // bits used in the last byte
}

func (bb *bitBuf) push(val, bits int) {
	for i := bits - 1; i >= 0; i-- {
		if bb.nbi == 0 {
			bb.b = append(bb.b, 0)
		}
		if val&(1<<i) != 0 {
			bb.b[len(bb.b)-1] |= byte(0x80 >> bb.nbi)
		}
		bb.nbi = (bb.nbi + 1) % 8
	}
}

// ---- encoding ----

// Encode builds the QR matrix for s: true = dark module.
func Encode(s string) ([][]bool, error) {
	data := []byte(s)
	version := 0
	for v := 1; v <= 20; v++ {
		if len(data) <= capacity(v) {
			version = v
			break
		}
	}
	if version == 0 {
		return nil, fmt.Errorf("payload too long for QR version 20 (%d bytes)", len(data))
	}

	countBits := 8
	if version >= 10 {
		countBits = 16
	}
	var bb bitBuf
	bb.push(0b0100, 4) // byte mode
	bb.push(len(data), countBits)
	for _, c := range data {
		bb.push(int(c), 8)
	}

	total := dataCodewords(version)
	// terminator (up to 4 zero bits) then pad to a byte boundary
	if rem := total*8 - (len(bb.b)*8 - (8-bb.nbi)%8); rem > 0 {
		t := 4
		if rem < 4 {
			t = rem
		}
		bb.push(0, t)
	}
	if bb.nbi != 0 {
		bb.push(0, 8-bb.nbi)
	}
	pad := []byte{0xEC, 0x11}
	for i := 0; len(bb.b) < total; i++ {
		bb.b = append(bb.b, pad[i%2])
	}

	// split into blocks, RS-encode each, then interleave
	nb := blocks[version]
	ecLen := ecPerBlock[version]
	short := total / nb
	long := total % nb // this many blocks carry one extra data codeword

	dataBlocks := make([][]byte, nb)
	ecBlocks := make([][]byte, nb)
	pos := 0
	for i := 0; i < nb; i++ {
		n := short
		if i >= nb-long {
			n++
		}
		dataBlocks[i] = bb.b[pos : pos+n]
		pos += n
		ecBlocks[i] = rsEncode(dataBlocks[i], ecLen)
	}
	var final []byte
	for i := 0; i < short+1; i++ {
		for _, blk := range dataBlocks {
			if i < len(blk) {
				final = append(final, blk[i])
			}
		}
	}
	for i := 0; i < ecLen; i++ {
		for _, blk := range ecBlocks {
			final = append(final, blk[i])
		}
	}

	return buildMatrix(version, final), nil
}

func buildMatrix(version int, codewords []byte) [][]bool {
	size := version*4 + 17
	m := make([][]bool, size)
	reserved := make([][]bool, size)
	for i := range m {
		m[i] = make([]bool, size)
		reserved[i] = make([]bool, size)
	}
	markFunctionModules(m, reserved, version)

	// data placement: two-column zigzag from the bottom right
	bitIdx := 0
	up := true
	for right := size - 1; right > 0; right -= 2 {
		if right == 6 {
			right = 5 // the vertical timing column is skipped
		}
		for i := 0; i < size; i++ {
			y := i
			if up {
				y = size - 1 - i
			}
			for c := 0; c < 2; c++ {
				x := right - c
				if reserved[y][x] {
					continue
				}
				dark := false
				if bitIdx < len(codewords)*8 {
					dark = codewords[bitIdx/8]&(0x80>>(bitIdx%8)) != 0
				}
				m[y][x] = dark
				bitIdx++
			}
		}
		up = !up
	}

	// pick the mask with the lowest penalty
	best, bestPenalty := 0, 1<<62
	var bestM [][]bool
	for mask := 0; mask < 8; mask++ {
		cand := applyMask(m, reserved, mask)
		writeFormat(cand, mask)
		if p := penalty(cand); p < bestPenalty {
			best, bestPenalty, bestM = mask, p, cand
		}
	}
	_ = best
	return bestM
}

// markFunctionModules draws every non-data element (finders, separators,
// timing, alignment, dark module, version info) and marks the format-info
// areas as reserved. Split out so the round-trip test can rebuild exactly
// the same reservation map.
func markFunctionModules(m, reserved [][]bool, version int) {
	size := version*4 + 17
	set := func(x, y int, dark bool) {
		if x < 0 || y < 0 || x >= size || y >= size {
			return
		}
		m[y][x] = dark
		reserved[y][x] = true
	}

	// finder patterns + separators
	for _, p := range [][2]int{{0, 0}, {size - 7, 0}, {0, size - 7}} {
		for dy := -1; dy <= 7; dy++ {
			for dx := -1; dx <= 7; dx++ {
				inRing := dx == 0 || dx == 6 || dy == 0 || dy == 6
				inCore := dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4
				border := dx == -1 || dx == 7 || dy == -1 || dy == 7
				set(p[0]+dx, p[1]+dy, !border && (inRing || inCore))
			}
		}
	}

	// timing patterns
	for i := 8; i < size-8; i++ {
		set(i, 6, i%2 == 0)
		set(6, i, i%2 == 0)
	}

	// alignment patterns (skipped where they would collide with finders)
	centers := alignCenters[version]
	for _, cy := range centers {
		for _, cx := range centers {
			if (cx == 6 && cy == 6) || (cx == 6 && cy == size-7) || (cx == size-7 && cy == 6) {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					ring := dx == -2 || dx == 2 || dy == -2 || dy == 2
					set(cx+dx, cy+dy, ring || (dx == 0 && dy == 0))
				}
			}
		}
	}

	// dark module and the reserved format-information strips
	set(8, size-8, true)
	for i := 0; i <= 8; i++ {
		if !reserved[i][8] {
			set(8, i, false)
		}
		if !reserved[8][i] {
			set(i, 8, false)
		}
	}
	for i := 0; i < 8; i++ {
		if !reserved[size-1-i][8] {
			set(8, size-1-i, false)
		}
		if !reserved[8][size-1-i] {
			set(size-1-i, 8, false)
		}
	}

	// version information (versions 7+)
	if version >= 7 {
		vi := versionBits(version)
		for i := 0; i < 18; i++ {
			bit := vi&(1<<i) != 0
			x, y := i/3, size-11+i%3
			set(x, y, bit)
			set(y, x, bit)
		}
	}
}

func applyMask(m, reserved [][]bool, mask int) [][]bool {
	size := len(m)
	out := make([][]bool, size)
	for y := range m {
		out[y] = make([]bool, size)
		copy(out[y], m[y])
		for x := range m[y] {
			if reserved[y][x] {
				continue
			}
			var flip bool
			switch mask {
			case 0:
				flip = (y+x)%2 == 0
			case 1:
				flip = y%2 == 0
			case 2:
				flip = x%3 == 0
			case 3:
				flip = (y+x)%3 == 0
			case 4:
				flip = (y/2+x/3)%2 == 0
			case 5:
				flip = (y*x)%2+(y*x)%3 == 0
			case 6:
				flip = ((y*x)%2+(y*x)%3)%2 == 0
			case 7:
				flip = ((y+x)%2+(y*x)%3)%2 == 0
			}
			if flip {
				out[y][x] = !out[y][x]
			}
		}
	}
	return out
}

// writeFormat places the BCH-protected format information (level L + mask).
func writeFormat(m [][]bool, mask int) {
	size := len(m)
	data := 0b01<<3 | mask // 01 = level L
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ (0x537 * ((rem >> 9) & 1))
	}
	bits := ((data<<10 | rem) ^ 0x5412) & 0x7FFF

	// Orientation matters and is easy to get backwards: in copy 1 the low
	// bits run DOWN column 8 and the high bits run LEFT along row 8; in
	// copy 2 it is the other way round — low bits along row 8 from the
	// right edge, high bits UP column 8 from the bottom.
	for i := 0; i < 15; i++ {
		bit := bits&(1<<i) != 0
		// copy 1, around the top-left finder
		switch {
		case i < 6:
			m[i][8] = bit
		case i == 6:
			m[7][8] = bit
		case i == 7:
			m[8][8] = bit
		case i == 8:
			m[8][7] = bit
		default:
			m[8][14-i] = bit
		}
		// copy 2, split between the other two finders
		if i < 8 {
			m[8][size-1-i] = bit
		} else {
			m[size-15+i][8] = bit
		}
	}
	m[size-8][8] = true // dark module
}

func versionBits(v int) int {
	rem := v
	for i := 0; i < 12; i++ {
		rem = (rem << 1) ^ (0x1F25 * ((rem >> 11) & 1))
	}
	return v<<12 | rem
}

// penalty implements the four scoring rules from the specification.
func penalty(m [][]bool) int {
	size := len(m)
	score := 0

	// rule 1: runs of five or more same-coloured modules
	for _, line := range [2]bool{true, false} {
		for a := 0; a < size; a++ {
			run, prev := 0, false
			for b := 0; b < size; b++ {
				var v bool
				if line {
					v = m[a][b]
				} else {
					v = m[b][a]
				}
				if b > 0 && v == prev {
					run++
					if run == 5 {
						score += 3
					} else if run > 5 {
						score++
					}
				} else {
					run = 1
				}
				prev = v
			}
		}
	}
	// rule 2: 2×2 blocks of one colour
	for y := 0; y < size-1; y++ {
		for x := 0; x < size-1; x++ {
			if m[y][x] == m[y][x+1] && m[y][x] == m[y+1][x] && m[y][x] == m[y+1][x+1] {
				score += 3
			}
		}
	}
	// rule 3: finder-like patterns
	pat1 := []bool{true, false, true, true, true, false, true, false, false, false, false}
	pat2 := []bool{false, false, false, false, true, false, true, true, true, false, true}
	match := func(get func(int) bool, start int, pat []bool) bool {
		for i, p := range pat {
			if get(start+i) != p {
				return false
			}
		}
		return true
	}
	for a := 0; a < size; a++ {
		for b := 0; b+11 <= size; b++ {
			row := func(i int) bool { return m[a][i] }
			col := func(i int) bool { return m[i][a] }
			if match(row, b, pat1) || match(row, b, pat2) {
				score += 40
			}
			if match(col, b, pat1) || match(col, b, pat2) {
				score += 40
			}
		}
	}
	// rule 4: deviation from an even dark/light balance
	dark := 0
	for _, row := range m {
		for _, v := range row {
			if v {
				dark++
			}
		}
	}
	pct := dark * 100 / (size * size)
	dev := pct - 50
	if dev < 0 {
		dev = -dev
	}
	score += (dev / 5) * 10
	return score
}

// ---- rendering ----

// PNG renders s as a QR code image. scale is the pixel size of one module;
// a 4-module quiet zone is added as the specification requires.
func PNG(s string, scale int) ([]byte, error) {
	m, err := Encode(s)
	if err != nil {
		return nil, err
	}
	if scale < 1 {
		scale = 1
	}
	const quiet = 4
	size := len(m)
	px := (size + quiet*2) * scale
	img := image.NewGray(image.Rect(0, 0, px, px))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if !m[y][x] {
				continue
			}
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.Set((x+quiet)*scale+dx, (y+quiet)*scale+dy, color.Gray{Y: 0})
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
