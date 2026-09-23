package handler

import (
	"math"
	"testing"
)

// Each of these fuzz targets feeds attacker-controlled MP4 box payloads into
// the parse functions. Two properties must always hold: the function never
// panics, and the result count never exceeds what the payload can actually
// hold (the forEachEntry bounding guard must not regress). Keep a crafted
// regression entry in every seed corpus.

func FuzzParseSTSS(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 0xFF, 0xFF, 0xFF, 0xFF}) // tiny payload, huge declared count
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0})             // zero entries
	f.Fuzz(func(t *testing.T, data []byte) {
		t.Parallel()
		out := parseSTSS(data)
		if len(out) > len(data)/4+2 {
			t.Fatalf("parseSTSS returned %d entries for %d bytes", len(out), len(data))
		}
	})
}

func FuzzParseSTCO(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		t.Parallel()
		out := parseSTCO(data)
		if len(out) > len(data)/4+2 {
			t.Fatalf("parseSTCO returned %d entries for %d bytes", len(out), len(data))
		}
	})
}

func FuzzParseCO64(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		t.Parallel()
		out := parseCO64(data)
		if len(out) > len(data)/8+2 {
			t.Fatalf("parseCO64 returned %d entries for %d bytes", len(out), len(data))
		}
	})
}

func FuzzParseSTSZ(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0xFF, 0xFF, 0xFF, 0xFF}) // huge sampleCount
	f.Add(make([]byte, 16))
	f.Fuzz(func(t *testing.T, data []byte) {
		t.Parallel()
		fixed, _, sizes := parseSTSZ(data)
		// When fixedSize == 0 the sizes slice must never exceed what fits
		// after the 12-byte header (a truncated/lying sampleCount is capped).
		if fixed == 0 {
			max := 0
			if len(data) > 12 {
				max = (len(data) - 12) / 4
			}
			if len(sizes) > max {
				t.Fatalf("parseSTSZ returned %d sizes for %d bytes, want <= %d", len(sizes), len(data), max)
			}
		} else if len(sizes) != 0 {
			t.Fatalf("parseSTSZ returned sizes with fixedSize=%d", fixed)
		}
	})
}

func FuzzParseSTTS(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 0xFF, 0xFF, 0xFF, 0xFF})
	f.Add(make([]byte, 8))
	f.Fuzz(func(t *testing.T, data []byte) {
		t.Parallel()
		out := parseSTTS(data)
		if len(out) > len(data)/8+2 {
			t.Fatalf("parseSTTS returned %d entries for %d bytes", len(out), len(data))
		}
	})
}

func FuzzParseSTSC(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 0xFF, 0xFF, 0xFF, 0xFF})
	f.Add(make([]byte, 8))
	f.Fuzz(func(t *testing.T, data []byte) {
		t.Parallel()
		out := parseSTSC(data)
		if len(out) > len(data)/12+2 {
			t.Fatalf("parseSTSC returned %d entries for %d bytes", len(out), len(data))
		}
	})
}

func FuzzFindAVCC(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0, 'z', 'z', 'z', 'z'}) // zero-size sample entry
	f.Add(make([]byte, 24))
	f.Fuzz(func(t *testing.T, data []byte) {
		t.Parallel()
		// Must return nil or a slice within the input, never panic or balloon.
		out := findAVCC(data)
		if out != nil && !(len(out) <= len(data)) {
			t.Fatalf("findAVCC returned %d bytes from %d-byte input", len(out), len(data))
		}
	})
}

func FuzzParseFFprobePTS(f *testing.F) {
	f.Add([]byte("0.000000\n10.416667\nNaN\nInf\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		t.Parallel()
		times := parseFFprobePTS(data)
		for i, v := range times {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("non-finite timestamp leaked at index %d: %v", i, v)
			}
		}
	})
}
