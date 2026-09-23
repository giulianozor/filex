package handler

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestExtractKeyframeJPEG(t *testing.T) {
	path := "/home/giuliano/Plex/bzl/Ok/transcoded_100408597.mp4"
	if _, err := os.Stat(path); err != nil {
		t.Skip("test file not available")
	}

	kfs, _, err := mp4Keyframes(path)
	if err != nil {
		t.Fatalf("mp4Keyframes: %v", err)
	}
	t.Logf("Found %d keyframes", len(kfs))

	// Test keyframes at various positions
	for _, idx := range []int{0, 1, 233, 499, 999} {
		if idx >= len(kfs) {
			continue
		}
		kf := kfs[idx]
		t.Run(fmt.Sprintf("KF%d-PTS=%.3f", idx+1, kf.PTS), func(t *testing.T) {
			start := time.Now()
			jpg, err := extractKeyframeJPEG(context.Background(), path, kf.PTS, 1)
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("extractKeyframeJPEG: %v", err)
			}
			t.Logf("Got %d bytes in %v, MD5=%x", len(jpg), elapsed, md5.Sum(jpg))

			if len(jpg) < 1000 {
				t.Fatalf("JPEG too small: %d bytes", len(jpg))
			}

			// Verify by running ffmpeg directly with same PTS
			cmd := exec.Command("ffmpeg",
				"-ss", fmt.Sprintf("%.6f", kf.PTS),
				"-i", path,
				"-frames:v", "1",
				"-q:v", "2",
				"-f", "image2pipe",
				"-vcodec", "mjpeg",
				"-",
			)
			refJPG, err := cmd.Output()
			if err != nil {
				t.Fatalf("ffmpeg verify: %v", err)
			}
			refMD5 := md5.Sum(refJPG)
			t.Logf("Verify MD5=%x", refMD5)

			if !bytes.Equal(jpg, refJPG) {
				os.WriteFile(fmt.Sprintf("/tmp/fail_verify_%03d_our.jpg", idx+1), jpg, 0644)
				os.WriteFile(fmt.Sprintf("/tmp/fail_verify_%03d_ref.jpg", idx+1), refJPG, 0644)
				t.Fatalf("images differ at KF %d (PTS=%.6f)\n  our: %x\n  ref: %x",
					idx+1, kf.PTS, md5.Sum(jpg), refMD5)
			}
		})
	}
}

func TestGenerateThumbnails_Bulk(t *testing.T) {
	path := "/home/giuliano/Plex/bzl/Ok/transcoded_100408597.mp4"
	if _, err := os.Stat(path); err != nil {
		t.Skip("test file not available")
	}

	dir := t.TempDir()

	start := time.Now()
	timestamps, err := generateKeyframeThumbnailsSample(context.Background(), path, dir, thumbFrameInterval, 4, 1, -1, -1, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("generateKeyframeThumbnailsSample: %v", err)
	}

	t.Logf("Generated %d thumbnails in %v (%.2f per second)",
		len(timestamps), elapsed, float64(len(timestamps))/elapsed.Seconds())

	if len(timestamps) == 0 {
		t.Fatal("no thumbnails generated")
	}

	var failures int
	for i, ts := range timestamps {
		name, _ := ts["name"].(string)
		timeVal, _ := ts["time"].(float64)

		info, err := os.Stat(dir + "/" + name)
		if err != nil {
			t.Fatalf("missing thumbnail %s: %v", name, err)
		}
		if info.Size() < 1000 {
			t.Errorf("thumbnail %d (%s) too small: %d bytes (PTS=%.6f)", i+1, name, info.Size(), timeVal)
			failures++
		}

		if i > 0 {
			prevTime, _ := timestamps[i-1]["time"].(float64)
			if timeVal <= prevTime {
				t.Errorf("non-monotonic PTS at index %d: %.6f <= %.6f", i, timeVal, prevTime)
				failures++
			}
		}
	}

	if failures > 0 {
		t.Fatalf("%d failures", failures)
	}

	t.Logf("First thumbnail PTS: %.6fs, last: %.6fs, duration: %.6fs",
		timestamps[0]["time"].(float64),
		timestamps[len(timestamps)-1]["time"].(float64),
		timestamps[len(timestamps)-1]["time"].(float64)-timestamps[0]["time"].(float64))
}
