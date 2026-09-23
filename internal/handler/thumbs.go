package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/giulianozor/filex/internal/atomicfile"
)

// sampleKeyframes picks keyframes so that no two are more than maxInterval
// seconds apart. If maxInterval <= 0, returns the full list unchanged.
func sampleKeyframes(kfs []KeyframeInfo, maxInterval float64) []KeyframeInfo {
	if maxInterval <= 0 || len(kfs) <= 1 {
		return kfs
	}
	sampled := make([]KeyframeInfo, 0, len(kfs)/2)
	var lastPTS float64
	for _, kf := range kfs {
		if len(sampled) == 0 || kf.PTS >= lastPTS+maxInterval {
			sampled = append(sampled, kf)
			lastPTS = kf.PTS
		}
	}
	return sampled
}

// countThumbnails scans keyframes and returns how many would be generated at
// the given sampling interval. Returns -1 if the scan fails.
func countThumbnails(absPath string, maxInterval float64) int {
	kfs, _, err := mp4Keyframes(absPath)
	if err != nil || len(kfs) == 0 {
		return -1
	}
	return len(sampleKeyframes(kfs, maxInterval))
}

// thumbFrameInterval is the maximum spacing in seconds between sampled
// keyframes when generating thumbnails. It is also the inferred spacing used
// when timestamps.json is missing and timestamps must be estimated from the
// on-disk frame order.
const thumbFrameInterval = 30.0

// thumbTimestampsFile is the cached per-video timestamp manifest stored next to
// the generated thumbnails.
const (
	thumbTimestampsFile = "timestamps.json"
)

// writeThumbTimestamps writes the thumbnail manifest into thumbDir atomically
// via the shared atomicfile writer, so a crash mid-write never leaves a
// truncated timestamps.json being read as a complete manifest. It is the single
// writer for both the partial manifests flushed during generation and the
// final one written on completion.
func writeThumbTimestamps(thumbDir string, entries []map[string]any, uid, gid int) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	manifest := filepath.Join(thumbDir, thumbTimestampsFile)
	if err := atomicfile.Write(manifest, data, 0o644); err != nil {
		return err
	}
	chownThumbFile(manifest, uid, gid)
	return nil
}

// chownThumbFile applies the jailed owner's uid/gid to a generated thumbnail
// artefact. Thumbnail generation is launched from the request handler with the
// server's own credentials (the goroutines cannot run inside RunAs), so the
// JPEGs and manifests it writes would otherwise end up root-owned inside a
// jail-owned directory, breaking later regeneration. Best-effort: an ownership
// fix-up failure is logged, not fatal.
func chownThumbFile(path string, uid, gid int) {
	if uid < 0 && gid < 0 {
		return
	}
	if err := os.Lchown(path, uid, gid); err != nil {
		log.Printf("thumbnail generation: chown %s: %v", path, err)
	}
}

// snapKeyframeWindow is the number of seconds scanned backwards from a
// requested time by snapKeyframeTimes when looking for the nearest keyframes.
const snapKeyframeWindow = 30.0

// snapKeyframeSpread is the number of seconds probed forwards past a requested
// time so the windowed keyframe scan always finds the next keyframe.
const snapKeyframeSpread = 10.0

// thumbFrameName returns the on-disk filename for the 1-based nth keyframe.
func thumbFrameName(n int) string {
	return fmt.Sprintf("%05d.jpg", n)
}

// clampThumbnailThreads resolves a configured per-ffmpeg thread count (0 =
// default) into a usable value. A negative or zero value maps to single-threaded
// decode, which is the safe default for a one-frame mjpeg pass.
func clampThumbnailThreads(configured int) int {
	if configured < 1 {
		return 1
	}
	return configured
}

// thumbEntryMap is the JSON shape of one thumbnail entry: on-disk frame name
// and PTS. It is shared by the timestamps manifest writes and the in-memory
// responses so both stay in sync.
func thumbEntryMap(name string, time float64) map[string]any {
	return map[string]any{"name": name, "time": time}
}

// effectiveParallelism resolves a configured parallelism value (0 = auto)
// into a concrete worker count, clamped to a sane range. The upper bound
// prevents a machine with dozens/hundreds of cores from spawning one ffmpeg
// process per half-core (each of which uses threads equal to the CPU count),
// which would exhaust memory on large libraries.
func effectiveParallelism(configured int) int {
	p := configured
	if p <= 0 {
		p = runtime.NumCPU() / 2
		if p < 2 {
			p = 2
		}
	}
	if p > maxThumbnailWorkers {
		return maxThumbnailWorkers
	}
	return p
}

// maxThumbnailWorkers caps how many parallel ffmpeg processes may extract
// thumbnails at once.
const maxThumbnailWorkers = 8

type progressFn func(written, total int) bool

func generateKeyframeThumbnailsSample(ctx context.Context, absPath string, thumbDir string, maxInterval float64, numWorkers int, threads int, uid, gid int, onProgress progressFn) ([]map[string]any, error) {
	// A caller-supplied zero/negative worker count would spawn no workers and
	// leave every job (and the producer) blocked until cancellation.
	if numWorkers < 1 {
		numWorkers = 1
	}
	kfs, _, err := mp4Keyframes(absPath)
	if err != nil {
		return nil, fmt.Errorf("keyframe scan: %w", err)
	}
	if len(kfs) == 0 {
		return nil, fmt.Errorf("no keyframes found")
	}

	kfs = sampleKeyframes(kfs, maxInterval)
	if len(kfs) == 0 {
		return nil, fmt.Errorf("no keyframes after sampling")
	}

	type job struct {
		idx int
		pts float64
	}
	type result struct {
		idx int
		jpg []byte
		err error
	}

	stopCh := make(chan struct{})
	jobs := make(chan job)
	results := make(chan result)

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				jpg, err := extractKeyframeJPEG(ctx, absPath, j.pts, threads)
				select {
				case results <- result{j.idx, jpg, err}:
				case <-ctx.Done():
					return
				case <-stopCh:
					return
				}
			}
		}()
	}

	// Collector goroutine writes each result to disk immediately so the
	// frontend sees thumbnails appear progressively. Each ordered slot tracks
	// whether its result has been received: entries whose frame extraction
	// failed (jpg==nil, err!=nil) must still be "consumed" so the writer can
	// move past them — otherwise a single failed frame at the head of the
	// sequence would stall every later frame forever.
	type collected struct {
		received bool
		jpg      []byte
		err      error
	}
	ordered := make([]collected, len(kfs))
	// frameWritten tracks slots whose JPEG actually reached the disk. A result
	// can carry a non-nil jpg yet still fail to write, so the manifest must be
	// driven by this flag, not by ordered[i].jpg alone.
	frameWritten := make([]bool, len(kfs))
	// writtenOK counts frames that actually landed on disk, so progress never
	// reports a failed/skipped frame as completed.
	writtenOK := 0

	var collectorWg sync.WaitGroup
	collectorWg.Add(1)
	go func() {
		defer collectorWg.Done()
		defer close(stopCh)
		nextIdx := 0
		writeNext := func() bool {
			for nextIdx < len(kfs) && ordered[nextIdx].received {
				if ctx.Err() != nil {
					return false
				}
				r := ordered[nextIdx]
				if r.err != nil {
					log.Printf("thumbnail generation: skipping keyframe %d (PTS=%.3f): %v", nextIdx, kfs[nextIdx].PTS, r.err)
					nextIdx++
					continue
				}
				frameName := thumbFrameName(nextIdx + 1)
				framePath := filepath.Join(thumbDir, frameName)
				// Write via the atomic temp+rename path so a crash or a
				// concurrent reader never sees a truncated .jpg.
				if err := atomicfile.Write(framePath, r.jpg, 0o644); err != nil {
					log.Printf("thumbnail generation: write %s: %v", frameName, err)
					nextIdx++
					continue
				}
				chownThumbFile(framePath, uid, gid)
				frameWritten[nextIdx] = true
				writtenOK++
				nextIdx++
				if onProgress != nil && !onProgress(writtenOK, len(kfs)) {
					return false
				}
			}
			return ctx.Err() == nil
		}

		flushTimestamps := func() {
			partial := make([]map[string]any, 0, nextIdx)
			for i := 0; i < nextIdx; i++ {
				// Only list frames whose JPEG actually landed on disk: entries
				// for extracted-but-failed frames (err != nil) or frames whose
				// write failed reference a .jpg that never exists.
				if !frameWritten[i] {
					continue
				}
				partial = append(partial, thumbEntryMap(thumbFrameName(i+1), kfs[i].PTS))
			}
			if err := writeThumbTimestamps(thumbDir, partial, uid, gid); err != nil {
				log.Printf("thumbnail generation: flush timestamps: %v", err)
			}
		}

		for {
			select {
			case <-ctx.Done():
				flushTimestamps()
				return
			case r, ok := <-results:
				if !ok {
					flushTimestamps()
					return
				}
				if r.idx >= 0 && r.idx < len(kfs) {
					ordered[r.idx] = collected{received: true, jpg: r.jpg, err: r.err}
				}
				if !writeNext() {
					flushTimestamps()
					return
				}
			}
		}
	}()

	for i, kf := range kfs {
		select {
		case <-ctx.Done():
			goto done
		case <-stopCh:
			goto done
		default:
		}
		select {
		case jobs <- job{i, kf.PTS}:
		case <-ctx.Done():
			goto done
		case <-stopCh:
			goto done
		}
	}
done:
	close(jobs)

	wg.Wait()
	close(results)
	collectorWg.Wait()

	// The collector flushed a partial manifest and stopCh before returning on
	// cancellation. Surface cancellation so the caller tears the directory down
	// instead of caching the partial result as a complete generation.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	timestamps := make([]map[string]any, 0, len(kfs))
	for i := 0; i < len(kfs); i++ {
		// Match flushTimestamps' predicate: a frame whose JPEG never landed on
		// disk (extraction or write failed, or it was dropped by a mid-batch
		// cancel) must not appear in the manifest, otherwise the served list
		// would point at a .jpg that does not exist.
		if !frameWritten[i] {
			continue
		}
		timestamps = append(timestamps, thumbEntryMap(thumbFrameName(i+1), kfs[i].PTS))
	}

	if len(timestamps) == 0 {
		os.Remove(filepath.Join(thumbDir, thumbTimestampsFile))
		return nil, fmt.Errorf("no thumbnails generated")
	}

	return timestamps, nil
}

// extractKeyframeJPEG renders one JPEG frame at pts. threads is expected to
// have already been clamped via clampThumbnailThreads by the caller (as every
// production call site and the unit tests do), so it is used verbatim.
// extractKeyframeJPEG renders one JPEG frame at pts. The thread count is
// clamped here as well as by callers so a direct caller can never hand a
// non-positive value to ffmpeg (which would make it use every core of the
// machine, once per worker).
func extractKeyframeJPEG(ctx context.Context, videoPath string, pts float64, threads int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-loglevel", "error",
		"-ss", formatDuration(pts),
		"-i", videoPath,
		"-frames:v", "1",
		"-q:v", "2",
		"-threads", fmt.Sprintf("%d", clampThumbnailThreads(threads)),
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, string(output))
	}
	if len(output) == 0 {
		return nil, fmt.Errorf("ffmpeg produced empty output at PTS %.6f", pts)
	}
	return output, nil
}
