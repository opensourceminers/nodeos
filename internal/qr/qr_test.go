package qr

import (
	"os"
	"testing"
)

// TestEncodeStructure checks the invariants a decoder relies on. Real
// verification is done by decoding the generated PNGs with zbarimg (see
// TestWriteSamples and the note in the package doc).
func TestEncodeStructure(t *testing.T) {
	for _, s := range []string{
		"hello",
		"bitcoin:bc1q8c0hfk6lrwcnxsrkrg7g40q0lnuha4mclspkca?amount=0.00042",
		"bitcoin:BC1Q8C0HFK6LRWCNXSRKRG7G40Q0LNUHA4MCLSPKCA?amount=0.0123&label=NodeOS&lightning=lnbc10u1p4x0qw0sp5dqm3afm5wt3qf0jaj4lldkdsfzvsnyzzxcztnkwluq9760j2xnkspp56qxyr7wfhkqmn9dcyy5",
	} {
		m, err := Encode(s)
		if err != nil {
			t.Fatalf("Encode(%d bytes): %v", len(s), err)
		}
		size := len(m)
		if (size-17)%4 != 0 {
			t.Fatalf("size %d is not a valid QR dimension", size)
		}
		// finder pattern cores must be dark in all three corners
		for _, p := range [][2]int{{3, 3}, {size - 4, 3}, {3, size - 4}} {
			if !m[p[1]][p[0]] {
				t.Errorf("finder core missing at %v (size %d)", p, size)
			}
		}
		// separator ring around the top-left finder must be light
		for i := 0; i < 8; i++ {
			if m[7][i] || m[i][7] {
				t.Errorf("separator not light at %d (size %d)", i, size)
			}
		}
		// timing pattern alternates starting dark at index 8
		for i := 8; i < size-8; i++ {
			if m[6][i] != (i%2 == 0) {
				t.Errorf("horizontal timing wrong at %d", i)
				break
			}
		}
		if !m[size-8][8] {
			t.Error("dark module missing")
		}
	}
}

func TestTooLong(t *testing.T) {
	long := make([]byte, 900)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := Encode(string(long)); err == nil {
		t.Fatal("expected an error for an oversized payload")
	}
}

// TestWriteSamples writes PNGs for external verification with a real
// decoder:  go test ./internal/qr -run Samples -v   then  zbarimg /tmp/qr-*.png
func TestWriteSamples(t *testing.T) {
	dir := os.Getenv("QR_SAMPLE_DIR")
	if dir == "" {
		t.Skip("set QR_SAMPLE_DIR to write sample PNGs")
	}
	samples := map[string]string{
		"short":  "hello nodeos",
		"bip21":  "bitcoin:bc1q8c0hfk6lrwcnxsrkrg7g40q0lnuha4mclspkca?amount=0.00042&label=NodeOS",
		"bolt11": "lnbc10u1p4x0qw0sp5dqm3afm5wt3qf0jaj4lldkdsfzvsnyzzxcztnkwluq9760j2xnkspp56qxyr7wfhkqmn9dcyy5xqrrsssp5",
		"unified": "bitcoin:BC1Q8C0HFK6LRWCNXSRKRG7G40Q0LNUHA4MCLSPKCA?amount=0.01234567&label=Autohaus%20Muster" +
			"&lightning=LNBC10U1P4X0QW0SP5DQM3AFM5WT3QF0JAJ4LLDKDSFZVSNYZZXCZTNKWLUQ9760J2XNKSPP56QXYR7WFHKQMN9DCYY5XQRRSSSP5",
	}
	for name, s := range samples {
		b, err := PNG(s, 6)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		path := dir + "/qr-" + name + ".png"
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		// plain-text matrix next to it, for diffing against a reference
		// implementation (qrencode -t ASCII -m 0)
		m, _ := Encode(s)
		var txt []byte
		for _, row := range m {
			for _, dark := range row {
				if dark {
					txt = append(txt, '#')
				} else {
					txt = append(txt, '.')
				}
			}
			txt = append(txt, '\n')
		}
		os.WriteFile(dir+"/qr-"+name+".txt", txt, 0o644)
		t.Logf("wrote %s (%d bytes payload)", path, len(s))
	}
}
