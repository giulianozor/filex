package handler

import (
	"math"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want string
	}{
		{"zero", 0, "00:00:00.000"},
		{"sub-second", 0.5, "00:00:00.500"},
		{"one-and-a-half hours", 3661.5, "01:01:01.500"},
		{"whole minutes", 120.0, "00:02:00.000"},
		{"negative clamped", -5.0, "00:00:00.000"},
		{"NaN clamped", math.NaN(), "00:00:00.000"},
		{"+Inf clamped", math.Inf(1), "00:00:00.000"},
		{"-Inf clamped", math.Inf(-1), "00:00:00.000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDuration(tt.in); got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestValidVideoSpan(t *testing.T) {
	tests := []struct {
		name       string
		start, end float64
		wantValid  bool
	}{
		{"normal", 0, 10, true},
		{"later", 10, 20, true},
		{"tiny", 0.5, 0.500001, true},
		{"negative start", -1, 5, false},
		{"NaN start", math.NaN(), 5, false},
		{"NaN end", 0, math.NaN(), false},
		{"+Inf end", 0, math.Inf(1), false},
		{"-Inf end", 0, math.Inf(-1), false},
		{"+Inf start", math.Inf(1), math.Inf(1), false},
		{"end equals start", 5, 5, false},
		{"end before start", 5, 4, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validVideoSpan(tt.start, tt.end); got != tt.wantValid {
				t.Errorf("validVideoSpan(%v, %v) = %v, want %v", tt.start, tt.end, got, tt.wantValid)
			}
		})
	}
}

func TestFirstInvalidSpan(t *testing.T) {
	if got := firstInvalidSpan(nil); got != -1 {
		t.Fatalf("empty spans: got index %d, want -1", got)
	}
	allValid := []timeSpan{{Start: 0, End: 1}, {Start: 1, End: 2}, {Start: 2, End: 3}}
	if got := firstInvalidSpan(allValid); got != -1 {
		t.Fatalf("all-valid spans: got index %d, want -1", got)
	}
	withBad := []timeSpan{{Start: 0, End: 1}, {Start: 1, End: 2}, {Start: 3, End: 2}}
	if got := firstInvalidSpan(withBad); got != 2 {
		t.Fatalf("invalid span at index 2: got index %d, want 2", got)
	}
}

func TestSampleKeyframes(t *testing.T) {
	kfs := []KeyframeInfo{
		{SampleNum: 1, PTS: 0},
		{SampleNum: 2, PTS: 1},
		{SampleNum: 3, PTS: 30},
		{SampleNum: 4, PTS: 60},
		{SampleNum: 5, PTS: 61},
	}

	sampled := sampleKeyframes(kfs, 30)
	if len(sampled) == 0 {
		t.Fatal("sampling must keep at least the first keyframe")
	}
	// PTS spacing must never exceed maxInterval for the sampled set.
	for i := 1; i < len(sampled); i++ {
		if d := sampled[i].PTS - sampled[i-1].PTS; d > 30+1e-9 {
			t.Errorf("sampled gap %.3f exceeds maxInterval 30", d)
		}
	}
	if len(sampled) == len(kfs) {
		t.Errorf("expected sampling to reduce dense keyframes, got all %d", len(kfs))
	}

	if got := sampleKeyframes(kfs, 0); len(got) != len(kfs) {
		t.Errorf("maxInterval<=0 must return input unchanged, got %d of %d", len(got), len(kfs))
	}
	if got := sampleKeyframes(nil, 30); len(got) != 0 {
		t.Errorf("empty input must stay empty, got %d", len(got))
	}
}

func TestParseFFprobePTS(t *testing.T) {
	out := []byte("0.000000\n10.416667\n\nnot-a-number\nNaN\nInf\n15.5\n")
	times := parseFFprobePTS(out)
	if len(times) != 3 {
		t.Fatalf("got %d times, want 3 (valid lines only): %v", len(times), times)
	}
	if times[0] != 0 || times[1] != 10.416667 || times[2] != 15.5 {
		t.Errorf("unexpected times: %v", times)
	}
	for i, v := range times {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("non-finite value leaked at index %d: %v", i, v)
		}
	}
}

func TestClampThumbnailThreads(t *testing.T) {
	tests := []struct {
		in, want int
	}{
		{0, 1}, {-3, 1}, {1, 1}, {4, 4},
	}
	for _, tt := range tests {
		if got := clampThumbnailThreads(tt.in); got != tt.want {
			t.Errorf("clampThumbnailThreads(%d) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

func TestEffectiveParallelism(t *testing.T) {
	if got := effectiveParallelism(0); got < 1 || got > maxThumbnailWorkers {
		t.Fatalf("auto parallelism %d out of range", got)
	}
	if got := effectiveParallelism(999); got != maxThumbnailWorkers {
		t.Fatalf("parallelism 999 = %d, want cap %d", got, maxThumbnailWorkers)
	}
	if got := effectiveParallelism(3); got != 3 {
		t.Fatalf("parallelism 3 = %d, want 3", got)
	}
}

func TestSingleOrList(t *testing.T) {
	rr := httptest.NewRecorder()
	if got, ok := singleOrList(rr, "", nil, "missing"); ok || got != nil {
		t.Fatalf("no input: got %v ok=%v, want nil false", got, ok)
	}
	rr = httptest.NewRecorder()
	if got, ok := singleOrList(rr, "/a", []string{"/b", "/c"}, "missing"); !ok || len(got) != 2 {
		t.Fatalf("list input: got %v ok=%v", got, ok)
	}
	rr = httptest.NewRecorder()
	if got, ok := singleOrList(rr, "/a", nil, "missing"); !ok || len(got) != 1 || got[0] != "/a" {
		t.Fatalf("single input: got %v ok=%v", got, ok)
	}
}

func TestBatchQueueHelpers(t *testing.T) {
	h := &Handler{batchQueue: make(map[string]map[string]struct{})}

	if h.batchQueued("u1", "a") {
		t.Fatal("fresh handler must report nothing queued")
	}
	if h.removeFromBatchQueue("u1", "a") {
		t.Fatal("removing an absent hash must report false")
	}

	h.enqueueBatch("u1", []string{"a", "b", "c"})
	for _, hh := range []string{"a", "b", "c"} {
		if !h.batchQueued("u1", hh) {
			t.Fatalf("hash %q should be queued after enqueue", hh)
		}
	}
	// Different user's bucket stays untouched.
	if h.batchQueued("u2", "a") {
		t.Fatal("u2 must not see u1's bucket")
	}

	if !h.removeFromBatchQueue("u1", "b") {
		t.Fatal("removing a queued hash must report true")
	}
	if h.batchQueued("u1", "b") {
		t.Fatal("hash b should be gone after removal")
	}

	// Drain the bucket; once empty the entry is deleted entirely.
	h.removeFromBatchQueue("u1", "a")
	h.removeFromBatchQueue("u1", "c")
	if _, exists := h.batchQueue["u1"]; exists {
		t.Fatal("empty bucket should be deleted from the queue")
	}
}

func TestHandleVideoSnapTime_NonFiniteOrNegative(t *testing.T) {
	h, _ := setupHandler(t)

	// Straight JSON number: negative time hits the explicit guard.
	rr := doRequest(t, h, "POST", "/api/video-snap-time",
		strings.NewReader(`{"path":"/x.mp4","time":-1}`))
	if rr.Code != 400 {
		t.Fatalf("negative time: status = %d, want 400 (%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "finite non-negative") {
		t.Fatalf("negative time: unexpected message %s", rr.Body.String())
	}

	// Go's json decoder rejects NaN/Inf literals and overflowing numbers, so
	// these must all be rejected as malformed too (400) rather than reaching
	// the handler logic.
	for _, body := range []string{
		`{"path":"/x.mp4","time":NaN}`,
		`{"path":"/x.mp4","time":Inf}`,
		`{"path":"/x.mp4","time":1e999}`,
	} {
		rr := doRequest(t, h, "POST", "/api/video-snap-time", strings.NewReader(body))
		if rr.Code != 400 {
			t.Errorf("body %s: status = %d, want 400", body, rr.Code)
		}
	}
}
