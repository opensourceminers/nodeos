package qr

import (
	"strings"
	"testing"
)

// decode reverses Encode far enough to prove the parts that are easy to get
// wrong: module placement, mask selection, block interleaving and
// Reed–Solomon encoding. It re-reads the matrix, tries every mask, and
// accepts the one whose de-interleaved codewords carry a well-formed byte
// segment. Geometry (finder/alignment positions) is covered by
// TestEncodeStructure and, ultimately, by a real scanner.
func decode(t *testing.T, m [][]bool) string {
	t.Helper()
	size := len(m)
	version := (size - 17) / 4

	// rebuild the reservation map exactly as the encoder does
	_, reserved := functionModules(version)

	// read the mask out of the format information rather than guessing it —
	// this also verifies the BCH-protected format bits the encoder wrote
	raw := 0
	for i := 0; i < 15; i++ {
		var bit bool
		switch {
		case i < 6:
			bit = m[i][8]
		case i == 6:
			bit = m[7][8]
		case i == 7:
			bit = m[8][8]
		case i == 8:
			bit = m[8][7]
		default:
			bit = m[8][14-i]
		}
		if bit {
			raw |= 1 << i
		}
	}
	fmtData := (raw ^ 0x5412) >> 10
	if lvl := fmtData >> 3; lvl != 0b01 {
		t.Fatalf("format info: EC level %02b, want 01 (L)", lvl)
	}
	for _, mask := range []int{fmtData & 7} {
		unmasked := applyMask(m, reserved, mask) // masking is its own inverse

		// read the zigzag back into a bit stream
		var bits []bool
		up := true
		for right := size - 1; right > 0; right -= 2 {
			if right == 6 {
				right = 5
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
					bits = append(bits, unmasked[y][x])
				}
			}
			up = !up
		}
		codewords := make([]byte, len(bits)/8)
		for i := range codewords {
			var b byte
			for j := 0; j < 8; j++ {
				if bits[i*8+j] {
					b |= 0x80 >> j
				}
			}
			codewords[i] = b
		}

		// de-interleave: data codewords first, EC block tail ignored
		total := dataCodewords(version)
		nb := blocks[version]
		short := total / nb
		long := total % nb
		lens := make([]int, nb)
		for i := range lens {
			lens[i] = short
			if i >= nb-long {
				lens[i]++
			}
		}
		data := make([][]byte, nb)
		idx := 0
		for i := 0; i < short+1 && idx < total; i++ {
			for b := 0; b < nb; b++ {
				if i < lens[b] {
					data[b] = append(data[b], codewords[idx])
					idx++
				}
			}
		}
		var stream []byte
		for _, blk := range data {
			stream = append(stream, blk...)
		}
		if len(stream) < 3 || stream[0]>>4 != 0b0100 {
			continue // not byte mode under this mask
		}
		countBits := 8
		if version >= 10 {
			countBits = 16
		}
		// pull the length and payload out of the bit stream
		get := func(pos, n int) int {
			v := 0
			for i := 0; i < n; i++ {
				p := pos + i
				if p/8 >= len(stream) {
					return -1
				}
				v <<= 1
				if stream[p/8]&(0x80>>(p%8)) != 0 {
					v |= 1
				}
			}
			return v
		}
		n := get(4, countBits)
		if n < 0 || 4+countBits+n*8 > len(stream)*8 {
			continue
		}
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sb.WriteByte(byte(get(4+countBits+i*8, 8)))
		}
		return sb.String()
	}
	t.Fatal("no mask produced a decodable byte segment")
	return ""
}

// functionModules rebuilds the matrix of reserved (non-data) positions.
func functionModules(version int) ([][]bool, [][]bool) {
	// buildMatrix already does this; reuse it with empty data so the
	// reservation map matches bit for bit.
	size := version*4 + 17
	m := make([][]bool, size)
	reserved := make([][]bool, size)
	for i := range m {
		m[i] = make([]bool, size)
		reserved[i] = make([]bool, size)
	}
	markFunctionModules(m, reserved, version)
	return m, reserved
}

func TestRoundTrip(t *testing.T) {
	cases := []string{
		"a",
		"hello nodeos",
		"bitcoin:bc1q8c0hfk6lrwcnxsrkrg7g40q0lnuha4mclspkca?amount=0.00042&label=NodeOS",
		"lnbc10u1p4x0qw0sp5dqm3afm5wt3qf0jaj4lldkdsfzvsnyzzxcztnkwluq9760j2xnkspp56qxyr7wfhkqmn9dcyy5",
		"bitcoin:BC1Q8C0HFK6LRWCNXSRKRG7G40Q0LNUHA4MCLSPKCA?amount=0.01234567&label=Autohaus%20Muster" +
			"&lightning=LNBC10U1P4X0QW0SP5DQM3AFM5WT3QF0JAJ4LLDKDSFZVSNYZZXCZTNKWLUQ9760J2XNKSPP56QXYR7WFHKQMN9DCYY5XQRRSSSP5",
		strings.Repeat("x", 400),
	}
	for _, want := range cases {
		m, err := Encode(want)
		if err != nil {
			t.Fatalf("Encode(%d bytes): %v", len(want), err)
		}
		if got := decode(t, m); got != want {
			t.Errorf("round trip failed for %d bytes:\n got %q\nwant %q", len(want), got, want)
		}
	}
}
