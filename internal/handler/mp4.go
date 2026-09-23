package handler

import (
	"encoding/binary"
	"fmt"
	"os"
)

// maxSampleScan bounds how many samples the sample-offset and PTS scan loops
// may process per request. Real videos rarely exceed a few million samples, so
// the cap only trips on malicious or corrupt metadata that would otherwise make
// a single request loop for minutes.
const maxSampleScan = uint64(10_000_000)

// maxMoovSize caps how many bytes of a declared 'moov' box are read into
// memory. Real movies track a few MB of metadata; the bound only trips on a
// corrupt/lying box size that would otherwise make the parse allocate (and
// copy from disk) up to the whole file size per request.
const maxMoovSize = 256 << 20 // 256 MiB

// maxKeyframes caps how many keyframe entries are materialised from the stss
// table. A crafted moov can declare tens of millions of stss entries within the
// moov size cap, and each one costs a KeyframeInfo plus a PTS; the bound stops
// that metadata from turning a single request into a multi-GB allocation.
const maxKeyframes = 1_000_000

type sttsEntry struct {
	count    uint32
	duration uint32
}

type stscEntry struct {
	firstChunk      uint32 // 1-indexed
	samplesPerChunk uint32
	sampleDescIndex uint32
}

// KeyframeInfo holds the exact location and timestamp of a keyframe.
type KeyframeInfo struct {
	SampleNum uint32  // 1-based MP4 sample number
	PTS       float64 // seconds
	Offset    int64   // byte offset of the access unit in the file
	Size      uint32  // byte size of the access unit
}

// mp4Keyframes parses the MP4 container to extract every keyframe's PTS and
// exact byte offset in the file. Also returns the avcC extradata (SPS/PPS) so
// callers can construct a valid H.264 bitstream. Returns an error on failure.
func mp4Keyframes(path string) ([]KeyframeInfo, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	fileSize := fi.Size()

	moovPos, moovSize, err := findMoov(f, fileSize)
	if err != nil {
		return nil, nil, err
	}
	if moovSize > maxMoovSize {
		return nil, nil, fmt.Errorf("moov box too large (%d bytes, cap %d)", moovSize, maxMoovSize)
	}

	moov := make([]byte, moovSize)
	if _, err := f.ReadAt(moov, moovPos); err != nil {
		return nil, nil, err
	}

	trakData := findVideoTrak(moov[boxHeaderLen(moov):])
	if trakData == nil {
		return nil, nil, fmt.Errorf("no video track found")
	}

	mdiaData := findBoxPayload(trakData, "mdia")
	if mdiaData == nil {
		return nil, nil, fmt.Errorf("no mdia box")
	}

	timeScale := parseMDHD(mdiaData)
	if timeScale == 0 {
		return nil, nil, fmt.Errorf("no time scale")
	}

	minfData := findBoxPayload(mdiaData, "minf")
	if minfData == nil {
		return nil, nil, fmt.Errorf("no minf box")
	}

	stblData := findBoxPayload(minfData, "stbl")
	if stblData == nil {
		return nil, nil, fmt.Errorf("no stbl box")
	}

	stts := parseSTTS(findBoxPayload(stblData, "stts"))
	if len(stts) == 0 {
		return nil, nil, fmt.Errorf("no stts entries")
	}

	stssSamples := parseSTSS(findBoxPayload(stblData, "stss"))
	if len(stssSamples) == 0 {
		return nil, nil, fmt.Errorf("no stss entries (no keyframes)")
	}
	if len(stssSamples) > maxKeyframes {
		return nil, nil, fmt.Errorf("too many keyframes (%d, cap %d)", len(stssSamples), maxKeyframes)
	}

	stco := parseSTCO(findBoxPayload(stblData, "stco"))
	if len(stco) == 0 {
		stco = parseCO64(findBoxPayload(stblData, "co64"))
	}
	if len(stco) == 0 {
		return nil, nil, fmt.Errorf("no stco/co64 entries")
	}

	stsc := parseSTSC(findBoxPayload(stblData, "stsc"))
	if len(stsc) == 0 {
		return nil, nil, fmt.Errorf("no stsc entries")
	}

	stszFixed, stszSampleCount, stsz := parseSTSZ(findBoxPayload(stblData, "stsz"))
	// stsz might be nil if fixedSize > 0 — handled in sampleOffset.

	avcc := findAVCC(stblData)

	totalSamples := stszSampleCount
	if stszFixed == 0 && totalSamples > uint32(len(stsz)) {
		// A crafted/truncated stsz can declare more samples than it carries.
		// Clamp to what was actually parsed so sampleOffset never maps past
		// the real size table onto bogus zero-size entries.
		totalSamples = uint32(len(stsz))
	}

	pts := computePTSFromSTTS(stssSamples, stts, timeScale)
	if len(pts) == 0 {
		return nil, nil, fmt.Errorf("could not compute PTS")
	}

	kfs := make([]KeyframeInfo, 0, len(stssSamples))
	for i, sampleNum := range stssSamples {
		if i >= len(pts) {
			break
		}
		off, size, ok := sampleOffset(sampleNum, stsc, stco, totalSamples, stszFixed, stsz)
		if !ok {
			// A keyframe whose offset/size cannot be derived (corrupt stsc,
			// out-of-range chunk, truncated stsz) is dropped rather than
			// reported as a bogus offset-0 access unit.
			continue
		}
		kfs = append(kfs, KeyframeInfo{
			SampleNum: sampleNum,
			PTS:       pts[i],
			Offset:    off,
			Size:      size,
		})
	}
	if len(kfs) == 0 {
		return nil, nil, fmt.Errorf("no usable keyframes")
	}

	return kfs, avcc, nil
}

// ---- stco / co64 ------------------------------------------------------------

// forEachEntry walks up to declaredCount entries of entrySize bytes each,
// starting at data[header:], capping the count by how many entries can
// actually fit in data. A small crafted box claiming billions of entries
// therefore cannot force a walk (or allocation) past the real payload. It is
// the shared walker behind every *_table parser (stco, co64, stsc, stsz,
// stts, stss), which otherwise duplicate the same bounded loop. declaredCount
// is read from the box header by the caller because its offset varies per
// table (stsz keeps more header fields before the count than the others).
func forEachEntry(data []byte, declaredCount uint32, header, entrySize int, fn func(entry []byte)) {
	declared := int(declaredCount)
	max := (len(data) - header) / entrySize
	if max < 0 {
		max = 0
	}
	if declared > max {
		declared = max
	}
	off := header
	for i := 0; i < declared && off+entrySize <= len(data); i++ {
		fn(data[off : off+entrySize])
		off += entrySize
	}
}

// boxEntryCount is the declared entry count stored at data[4:8], the common
// header position for most sample tables.
func boxEntryCount(data []byte) uint32 {
	return binary.BigEndian.Uint32(data[4:8])
}

// tableCapacity returns a safe initial capacity for a sample-table slice backed
// by data: the number of entrySize-byte entries that fit after header, capped
// at maxSampleScan. Without the cap a 256 MiB moov could pre-allocate hundreds
// of MB per table before forEachEntry's declared-count walk clamps anything.
func tableCapacity(dataLen, header, entrySize int) int {
	n := (dataLen - header) / entrySize
	if n < 0 {
		n = 0
	}
	if n > int(maxSampleScan) {
		n = int(maxSampleScan)
	}
	return n
}

// parseChunkOffsets reads entryCount big-endian chunk offsets from data, each
// entrySize bytes wide, using read to decode a single offset.
func parseChunkOffsets(data []byte, entrySize int, read func([]byte) int64) []int64 {
	if len(data) < 8 {
		return nil
	}
	offsets := make([]int64, 0, tableCapacity(len(data), 8, entrySize))
	forEachEntry(data, boxEntryCount(data), 8, entrySize, func(e []byte) {
		offsets = append(offsets, read(e))
	})
	return offsets
}

func parseSTCO(data []byte) []int64 {
	return parseChunkOffsets(data, 4, func(b []byte) int64 { return int64(binary.BigEndian.Uint32(b)) })
}

func parseCO64(data []byte) []int64 {
	return parseChunkOffsets(data, 8, func(b []byte) int64 { return int64(binary.BigEndian.Uint64(b)) })
}

// ---- stsc -------------------------------------------------------------------

func parseSTSC(data []byte) []stscEntry {
	if len(data) < 8 {
		return nil
	}
	entries := make([]stscEntry, 0, tableCapacity(len(data), 8, 12))
	forEachEntry(data, boxEntryCount(data), 8, 12, func(e []byte) {
		entries = append(entries, stscEntry{
			firstChunk:      binary.BigEndian.Uint32(e),
			samplesPerChunk: binary.BigEndian.Uint32(e[4:]),
			sampleDescIndex: binary.BigEndian.Uint32(e[8:]),
		})
	})
	return entries
}

// ---- stsz -------------------------------------------------------------------

func parseSTSZ(data []byte) (fixedSize uint32, sampleCount uint32, sizes []uint32) {
	if len(data) < 12 {
		return 0, 0, nil
	}
	fixedSize = binary.BigEndian.Uint32(data[4:8])
	sampleCount = binary.BigEndian.Uint32(data[8:12])
	if fixedSize > 0 {
		return fixedSize, sampleCount, nil
	}
	sizes = make([]uint32, 0, tableCapacity(len(data), 12, 4))
	forEachEntry(data, sampleCount, 12, 4, func(e []byte) {
		sizes = append(sizes, binary.BigEndian.Uint32(e))
	})
	return 0, sampleCount, sizes
}

// ---- avcC -------------------------------------------------------------------

// findAVCC locates the avcC box inside stbl > stsd > avc1 > avcC.
func findAVCC(stblData []byte) []byte {
	stsdData := findBoxPayload(stblData, "stsd")
	if len(stsdData) < 8 {
		return nil
	}
	entryCount := binary.BigEndian.Uint32(stsdData[4:8])
	off := 8
	for i := uint32(0); i < entryCount; i++ {
		if off+8 > len(stsdData) {
			break
		}
		entrySize := int(binary.BigEndian.Uint32(stsdData[off:]))
		if entrySize < 8 {
			// Malformed sample entry: size must be at least the 8-byte header.
			break
		}
		if !typeAt(stsdData, off+4, "avc1") && !typeAt(stsdData, off+4, "h264") {
			off += entrySize
			continue
		}
		payloadEnd := off + entrySize
		if payloadEnd > len(stsdData) {
			payloadEnd = len(stsdData)
		}
		entryPayload := stsdData[off+8 : payloadEnd]
		// Scan for a box of type "avcC" inside the sample entry payload.
		// The fixed-size VisualSampleEntry header comes before the sub-box
		// region, but its size can vary; we just search for the "avcC" string
		// preceded by a valid 4-byte big-endian size.
		for j := 0; j+8 <= len(entryPayload); j++ {
			if !typeAt(entryPayload, j+4, "avcC") {
				continue
			}
			boxSize := int(binary.BigEndian.Uint32(entryPayload[j:]))
			if boxSize >= 8 && j+boxSize <= len(entryPayload) {
				return entryPayload[j+8 : j+boxSize]
			}
		}
		off += entrySize
	}
	return nil
}

// ---- helpers ----------------------------------------------------------------

// typeAt reports whether the 4-byte box type located at b[off:off+4] equals
// the four characters of tag. Unlike string slicing, it performs no allocation.
func typeAt(b []byte, off int, tag string) bool {
	if off < 0 || off+4 > len(b) {
		return false
	}
	return b[off] == tag[0] && b[off+1] == tag[1] && b[off+2] == tag[2] && b[off+3] == tag[3]
}

// sampleOffset computes the file offset and byte size for a 1-indexed sample
// number. The bool result is false when the sample cannot be located (corrupt
// stsc/stco/stsz or an out-of-range sample), so callers can drop the entry
// instead of treating (0,0) as a valid access unit.
func sampleOffset(sampleNum uint32, stsc []stscEntry, stco []int64, totalSamples uint32, stszFixed uint32, stszSizes []uint32) (int64, uint32, bool) {
	if sampleNum < 1 || sampleNum > totalSamples {
		return 0, 0, false
	}

	// Walk stsc entries to find which chunk contains sampleNum
	// Accumulators hold up to total file size: use 64-bit so offsets stay
	// correct for chunks whose combined sample data exceeds 4GB. samplesProcessed
	// is also kept 64-bit: a single crafted stsc range can span more than 2^32
	// samples, and wrapping it here would map sampleNum onto the wrong chunk.
	samplesProcessed := uint64(0)

	for i := 0; i < len(stsc); i++ {
		firstChunk := stsc[i].firstChunk // 1-indexed
		var lastChunk uint32
		if i+1 < len(stsc) {
			lastChunk = stsc[i+1].firstChunk - 1
		} else {
			lastChunk = uint32(len(stco))
		}

		if firstChunk < 1 || firstChunk > uint32(len(stco)) {
			continue
		}
		if lastChunk > uint32(len(stco)) {
			lastChunk = uint32(len(stco))
		}

		spc := stsc[i].samplesPerChunk
		if spc == 0 {
			continue
		}
		if lastChunk < firstChunk {
			// Malformed/unsorted stsc: lastChunk - firstChunk + 1 would wrap
			// and turn this range into ~2^64 phantom samples.
			continue
		}
		chunksInRange := lastChunk - firstChunk + 1
		samplesInRange := uint64(chunksInRange) * uint64(spc)
		newProcessed := samplesProcessed + samplesInRange
		// Malicious stsc metadata can declare a huge per-chunk sample count; the
		// sizeBefore scan below costs one iteration per sample, so bail early.
		if newProcessed > maxSampleScan {
			return 0, 0, false
		}

		if uint64(sampleNum) <= newProcessed {
			localSample := uint64(sampleNum) - samplesProcessed // 1-indexed within this stsc range
			chunkIdx := (localSample - 1) / uint64(spc)         // 0-indexed within range

			chunkAbsolute := uint32(uint64(firstChunk) + chunkIdx - 1) // 0-indexed overall chunk index
			if chunkAbsolute >= uint32(len(stco)) {
				return 0, 0, false
			}
			chunkOffset := stco[chunkAbsolute]
			// co64 offsets are decoded from an unsigned 64-bit field; a
			// crafted/negative value (or a zero offset, which can never be a
			// sample) would otherwise flow into KeyframeInfo.Offset.
			if chunkOffset <= 0 {
				return 0, 0, false
			}

			// First sample of this chunk (1-indexed overall)
			firstSampleOfChunk := samplesProcessed + chunkIdx*uint64(spc) + 1

			// Sum sizes of samples before this one within the chunk
			var sizeBefore int64
			for s := firstSampleOfChunk; s < uint64(sampleNum); s++ {
				if stszFixed > 0 {
					sizeBefore += int64(stszFixed)
				} else if s-1 < uint64(len(stszSizes)) {
					sizeBefore += int64(stszSizes[s-1])
				}
			}

			// Size of this sample
			var sampleSize uint32
			if stszFixed > 0 {
				sampleSize = stszFixed
			} else if int(sampleNum-1) < len(stszSizes) {
				sampleSize = stszSizes[sampleNum-1]
			}

			return chunkOffset + sizeBefore, sampleSize, true
		}

		samplesProcessed += samplesInRange
	}

	return 0, 0, false
}

// ---- existing parsing (unchanged below this line) ---------------------------

func findMoov(f *os.File, fileSize int64) (pos int64, size int64, err error) {
	var header [16]byte
	offset := int64(0)

	for offset+8 <= fileSize {
		if _, err := f.ReadAt(header[:8], offset); err != nil {
			return 0, 0, err
		}
		boxSize := int64(binary.BigEndian.Uint32(header[0:4]))

		if boxSize == 1 {
			if _, err := f.ReadAt(header[8:16], offset+8); err != nil {
				return 0, 0, err
			}
			boxSize = int64(binary.BigEndian.Uint64(header[8:16]))
		} else if boxSize == 0 {
			boxSize = fileSize - offset
		}

		// The minimum valid size depends on the header form: a largesize box
		// (size field == 1) needs at least 16 bytes, not 8. Rejecting only
		// boxSize < 8 let an 8-byte largesize through, after which slicing off
		// the 16-byte header panicked.
		if boxSize < int64(boxHeaderLen(header[:16])) || offset+boxSize > fileSize {
			return 0, 0, fmt.Errorf("invalid box at %d: size=%d", offset, boxSize)
		}

		if typeAt(header[:16], 4, "moov") {
			return offset, boxSize, nil
		}
		offset += boxSize
	}
	return 0, 0, fmt.Errorf("moov not found (scanned to offset %d of %d)", offset, fileSize)
}

func findVideoTrak(moov []byte) []byte {
	// pred receives the whole box (header included); strip the header before
	// handing the trak payload to isVideoTrak, which treats it as a container.
	return findFirstBox(moov, func(box []byte) bool {
		return isVideoTrak(box[boxHeaderLen(box):])
	})
}

func isVideoTrak(trakData []byte) bool {
	mdiaData := findBoxPayload(trakData, "mdia")
	if mdiaData == nil {
		return false
	}
	hdlrData := findBoxPayload(mdiaData, "hdlr")
	if len(hdlrData) < 12 {
		return false
	}
	// hdlr is a FullBox: version/flags (4) + pre_defined (4) + handler_type (4).
	return typeAt(hdlrData, 8, "vide")
}

// findBoxPayload returns the payload of the first child box in data whose type
// is boxType, or nil when absent. Malformed boxes stop the walk instead of
// reading out of bounds.
func findBoxPayload(data []byte, boxType string) []byte {
	// The box type always sits at offset 4 of the full box (header included),
	// so the predicate must inspect the whole box rather than just its payload.
	return findFirstBox(data, func(box []byte) bool { return typeAt(box, 4, boxType) })
}

// boxHeaderLen returns the header length of the box at the start of data: a
// size field of 1 means a 64-bit largesize follows, giving a 16-byte header.
func boxHeaderLen(data []byte) int {
	if len(data) >= 4 && binary.BigEndian.Uint32(data) == 1 {
		return 16
	}
	return 8
}

// findFirstBox walks data as an MP4 container and returns the payload (bytes
// after the box header) of the first box for which pred reports true. pred
// receives the whole box, header included, so it can inspect the type at
// offset 4. It is the shared walker behind findBoxPayload and findVideoTrak.
// Both size==0 (extends to the container end) and size==1 (64-bit largesize)
// child headers are handled.
func findFirstBox(data []byte, pred func(box []byte) bool) []byte {
	for offset := 0; offset+8 <= len(data); {
		size := int64(binary.BigEndian.Uint32(data[offset:]))
		switch size {
		case 0:
			size = int64(len(data) - offset)
		case 1:
			if offset+16 > len(data) {
				return nil
			}
			size = int64(binary.BigEndian.Uint64(data[offset+8:]))
		}
		if size < int64(boxHeaderLen(data[offset:])) || int64(offset)+size > int64(len(data)) {
			return nil
		}
		box := data[offset : int64(offset)+size]
		if pred(box) {
			return box[boxHeaderLen(box):]
		}
		offset += int(size)
	}
	return nil
}

func parseMDHD(mdiaData []byte) uint32 {
	mdhdData := findBoxPayload(mdiaData, "mdhd")
	if len(mdhdData) < 12 {
		return 0
	}
	version := mdhdData[0]
	if version == 0 {
		if len(mdhdData) < 16 {
			return 0
		}
		return binary.BigEndian.Uint32(mdhdData[12:16])
	}
	if len(mdhdData) < 24 {
		return 0
	}
	return binary.BigEndian.Uint32(mdhdData[20:24])
}

func parseSTTS(data []byte) []sttsEntry {
	if len(data) < 8 {
		return nil
	}
	entries := make([]sttsEntry, 0, tableCapacity(len(data), 8, 8))
	forEachEntry(data, boxEntryCount(data), 8, 8, func(e []byte) {
		entries = append(entries, sttsEntry{
			count:    binary.BigEndian.Uint32(e),
			duration: binary.BigEndian.Uint32(e[4:]),
		})
	})
	return entries
}

func parseSTSS(data []byte) []uint32 {
	if len(data) < 8 {
		return nil
	}
	samples := make([]uint32, 0, tableCapacity(len(data), 8, 4))
	forEachEntry(data, boxEntryCount(data), 8, 4, func(e []byte) {
		samples = append(samples, binary.BigEndian.Uint32(e))
	})
	return samples
}

func computePTSFromSTTS(stssSamples []uint32, sttsEntries []sttsEntry, timeScale uint32) []float64 {
	if timeScale == 0 || len(stssSamples) == 0 || len(sttsEntries) == 0 {
		return nil
	}
	entries := make([]sttsEntry, len(sttsEntries))
	copy(entries, sttsEntries)

	pts := make([]float64, 0, len(stssSamples))
	sampleIdx := uint32(1)
	accumulated := uint64(0)

	for _, sampleNum := range stssSamples {
		// The stss list must be strictly ascending: a duplicate or descending
		// entry would not match the "advance one sample per keyframe" model and
		// would emit a duplicated/regressed PTS while the cursor drifts.
		if sampleNum < sampleIdx {
			return nil
		}
		// A corrupt stts can declare a huge count for one entry; stepping past
		// it sample-by-sample would spin for minutes. Bail instead.
		for sampleIdx < sampleNum {
			if uint64(sampleIdx) >= maxSampleScan {
				return nil
			}
			if len(entries) == 0 {
				// The stts table ran out before reaching this keyframe: its
				// timestamp cannot be derived, and silently reusing the last
				// accumulated value would produce a flat, wrong timeline.
				return nil
			}
			accumulated = advanceSTTS(&entries, accumulated)
			sampleIdx++
		}

		pts = append(pts, float64(accumulated)/float64(timeScale))

		if len(entries) > 0 {
			accumulated = advanceSTTS(&entries, accumulated)
			sampleIdx++
		}
	}

	return pts
}

// advanceSTTS consumes entries' first run (adding its duration to accumulated)
// and removes the run when it is exhausted. It is the sample-pointer step
// duplicated by the forward and the keyframe hits in computePTSFromSTTS.
func advanceSTTS(entries *[]sttsEntry, accumulated uint64) uint64 {
	// A corrupt entry with count==0 encodes no samples: consume it without
	// adding a duration. Decrementing it instead would wrap the uint32 to
	// 0xFFFFFFFF and spin until the maxSampleScan tripwire.
	if (*entries)[0].count == 0 {
		*entries = (*entries)[1:]
		return accumulated
	}
	accumulated += uint64((*entries)[0].duration)
	(*entries)[0].count--
	if (*entries)[0].count == 0 {
		*entries = (*entries)[1:]
	}
	return accumulated
}
