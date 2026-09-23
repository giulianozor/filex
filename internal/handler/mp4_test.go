package handler

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestMP4Keyframes(t *testing.T) {
	kfs, _, err := mp4Keyframes("/home/giuliano/Plex/bzl/transcoded_12cfa8d8d6a04c79957e86e3d747d649.mp4")
	if err != nil || len(kfs) == 0 {
		t.Skip("test file not available")
	}
	pts := make([]float64, len(kfs))
	for i, kf := range kfs {
		pts[i] = kf.PTS
	}

	if len(pts) < 2 {
		t.Fatalf("expected at least 2 keyframes, got %d", len(pts))
	}

	if pts[0] != 0 {
		t.Errorf("first keyframe PTS should be 0, got %f", pts[0])
	}

	// GOP should be ~10.416s (250 frames at 24fps)
	gop := pts[1] - pts[0]
	if math.Abs(gop-10.416667) > 0.001 {
		t.Errorf("expected GOP ~10.416667, got %f", gop)
	}

	// Verify monotonic
	for i := 1; i < len(pts); i++ {
		if pts[i] <= pts[i-1] {
			t.Errorf("PTS not monotonic at index %d: %f <= %f", i, pts[i], pts[i-1])
		}
	}

	t.Logf("parsed %d keyframes, GOP=%.6fs, last PTS=%.6f", len(pts), pts[1]-pts[0], pts[len(pts)-1])
}

// TestFindAVCC_ZeroSizeEntry guards against an infinite loop (and hang) when an
// stsd sample entry advertises a size smaller than its 8-byte header (0 bytes).
func TestFindAVCC_ZeroSizeEntry(t *testing.T) {
	// Build an stsd box whose payload declares 1 entry with size 0.
	stsdPayload := make([]byte, 8)
	binary.BigEndian.PutUint32(stsdPayload[4:8], 1) // entryCount = 1
	// First (only) sample entry: size = 0, type "zzzz" (not avc1).
	stsdPayload = append(stsdPayload, 0, 0, 0, 0, 'z', 'z', 'z', 'z')

	// Build an stbl box wrapping the stsd payload.
	stsdBox := make([]byte, 8)
	binary.BigEndian.PutUint32(stsdBox[0:4], uint32(len(stsdPayload)+8))
	copy(stsdBox[4:8], "stsd")
	stbl := append(stsdBox, stsdPayload...)

	if got := findAVCC(stbl); got != nil {
		t.Fatalf("expected nil for malformed zero-size entry, got %v", got)
	}
}

// mp4Box builds a size/type box wrapping payload using an 8-byte header. A
// negative headerLen selects the 64-bit (size==1 + largesize) header form.
func mp4Box(typ string, headerLen int, payload []byte) []byte {
	total := headerLen + len(payload)
	box := make([]byte, headerLen)
	if headerLen == 16 {
		binary.BigEndian.PutUint32(box[0:4], 1)
		binary.BigEndian.PutUint64(box[8:16], uint64(total))
	} else {
		binary.BigEndian.PutUint32(box[0:4], uint32(total))
	}
	copy(box[4:8], typ)
	return append(box, payload...)
}

// TestFindBoxPayload is the regression guard for the walker that every
// downstream MP4 lookup (mdia/minf/stbl/stsd/...) depends on. findBoxPayload
// previously inspected the payload at a negative offset and thus never matched,
// silently disabling keyframe scanning.
func TestFindBoxPayload(t *testing.T) {
	inner := mp4Box("mdhd", 8, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	mdia := mp4Box("mdia", 8, inner)
	trak := mp4Box("trak", 8, mdia)

	// findBoxPayload walks a container payload, so pass trak's payload (the
	// mdia box) and expect mdia's own payload (the mdhd box).
	got := findBoxPayload(trak[boxHeaderLen(trak):], "mdia")
	if got == nil {
		t.Fatal("findBoxPayload(trak payload, mdia) = nil, want payload")
	}
	if !bytes.Equal(got, inner) {
		t.Fatalf("findBoxPayload returned %d bytes, want the mdia payload (inner, %d)", len(got), len(inner))
	}
	if findBoxPayload(trak[boxHeaderLen(trak):], "nope") != nil {
		t.Fatal("findBoxPayload matched an absent box type")
	}
}

// TestFindFirstBox_64BitSize verifies the walker decodes size==1 headers
// (64-bit largesize) instead of aborting the container walk.
func TestFindFirstBox_64BitSize(t *testing.T) {
	inner := mp4Box("mdhd", 16, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	mdia := mp4Box("mdia", 16, inner)
	trak := mp4Box("trak", 16, mdia)

	got := findBoxPayload(trak[boxHeaderLen(trak):], "mdia")
	if !bytes.Equal(got, inner) {
		t.Fatalf("64-bit findBoxPayload returned %v, want mdia payload", got)
	}
}

// TestFindVideoTrak_HdlrOffset verifies hdlr's handler_type is read at payload
// offset 8 (version/flags + pre_defined precede it), and that a non-video
// handler is rejected.
func TestFindVideoTrak_HdlrOffset(t *testing.T) {
	hdlrPayload := make([]byte, 12)
	copy(hdlrPayload[8:12], "vide")
	hdlr := mp4Box("hdlr", 8, hdlrPayload)
	mdia := mp4Box("mdia", 8, hdlr)
	trak := mp4Box("trak", 8, mdia)

	if !isVideoTrak(mdia) {
		t.Fatal("isVideoTrak returned false for a vide handler")
	}
	if !bytes.Equal(findVideoTrak(trak), mdia) {
		t.Fatal("findVideoTrak did not return the video trak payload")
	}

	copy(hdlrPayload[8:12], "soun")
	if isVideoTrak(mp4Box("mdia", 8, mp4Box("hdlr", 8, hdlrPayload))) {
		t.Fatal("isVideoTrak accepted a soun handler")
	}
}

// TestAdvanceSTTS_ZeroCount guards the uint32 underflow that turned a corrupt
// count==0 entry into an effectively infinite scan.
func TestAdvanceSTTS_ZeroCount(t *testing.T) {
	entries := []sttsEntry{{count: 0, duration: 7}, {count: 2, duration: 5}}
	if got := advanceSTTS(&entries, 0); got != 0 {
		t.Fatalf("advanceSTTS on count==0 = %d, want 0 (entry consumed, no duration)", got)
	}
	if len(entries) != 1 || entries[0].count != 2 {
		t.Fatalf("advanceSTTS left %+v, want one entry with count 2", entries)
	}
}

// TestBoundedEntries_TruncatedData guards the allocation DoS path: a box
// header declaring billions of entries must be capped to what actually fits in
// the payload before the parser sizes a slice from it.
func TestBoundedEntries_TruncatedData(t *testing.T) {
	data := make([]byte, 16)                          // tiny payload
	binary.BigEndian.PutUint32(data[4:8], 0xFFFFFFFF) // entryCount max uint32
	if got := parseSTSS(data); len(got) != 2 {        // (16-8)/4 = 2 entries fit
		t.Fatalf("parseSTSS with huge declared count: got %d entries, want 2", len(got))
	}
	if got := parseSTCO(data); len(got) != 2 {
		t.Fatalf("parseSTCO with huge declared count: got %d entries, want 2", len(got))
	}

	big := make([]byte, 8+4+4)
	binary.BigEndian.PutUint32(big[8:12], 0xFFFFFFFF) // fixedSize == 0, sampleCount huge
	// fits exactly one size entry after the 12-byte header
	if _, _, sizes := parseSTSZ(big); len(sizes) != 1 {
		t.Fatalf("parseSTSZ with huge sampleCount: got %d sizes, want 1", len(sizes))
	}
}
