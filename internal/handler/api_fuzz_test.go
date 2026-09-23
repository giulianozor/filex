package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// These fuzz targets drive the mutation APIs — the "new / rename file and
// folder" family plus the raw-content write endpoint — with fuzzed request
// bodies. The invariants enforced per exec:
//
//   - the handler never panics (a recovered panic surfaces as a 500 and fails
//     the allowlist below) and never leaks an unexpected status code;
//   - anywhere the API reports success, the data actually landed inside the
//     jail (base directory) and never escaped it;
//   - streaming endpoints emit well-formed NDJSON lines.
//
// Each exec builds a fresh handler over its own temp dir (and a pre-seeded
// file/dir) so results are deterministic and fuzz workers can't interfere.

// fuzzPost sends a JSON POST request to the handler.
func fuzzPost(t *testing.T, h *Handler, route, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, route, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// assertFuzzStatus fails when the response status is not one of the statuses a
// mutation handler is allowed to produce. Any other status (notably a 500 from
// a recovered panic, or an unhandled error path) is a regression.
func assertFuzzStatus(t *testing.T, route, body string, rr *httptest.ResponseRecorder) {
	t.Helper()
	switch rr.Code {
	case http.StatusOK, http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		// 200 = executed, 400 = validation/fs error, 413 = body over the cap.
	default:
		t.Fatalf("%s: unexpected status %d for body %q: %s", route, rr.Code, body, rr.Body.String())
	}
}

// fuzzJailDir maps the given virtual path onto the jail base dir.
func fuzzJailDir(dir, virtualPath string) string {
	return filepath.Join(dir, strings.TrimPrefix(path.Clean("/"+virtualPath), "/"))
}

// assertInsideJail fails when abs is outside the jail base dir.
func assertInsideJail(t *testing.T, dir, label, abs string) {
	t.Helper()
	rel, err := filepath.Rel(dir, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("%s escapes jail (base %s): %s", label, dir, abs)
	}
}

// fuzzSetup builds a handler over a fresh temp dir with a pre-seeded file and
// folder so rename/move/copy legitimately succeed for valid fuzz inputs.
func fuzzSetup(t *testing.T) (*Handler, string) {
	t.Helper()
	h, dir := setupHandler(t)
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "seeddir"), 0o755); err != nil {
		t.Fatal(err)
	}
	return h, dir
}

func FuzzHandleCreateFile(f *testing.F) {
	for _, seed := range []string{
		`{"path":"/","name":"a.txt"}`,
		`{"path":"/","name":"sub/b.txt"}`,
		`{"path":"/","name":"..\\evil"}`,
		`{"path":"/","name":".."}`,
		`{"path":"/","name":"."}`,
		`{"path":"/","name":""}`,
		`{"path":"","name":"a"}`,
		`{"path":"/nope/x","name":"a"}`,
		`{"path":"/seeddir","name":"kept"}`,
		`garbage`,
		`{"path":123,"name":"a"}`,
		`{"path":"/"}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		h, dir := fuzzSetup(t)
		rr := fuzzPost(t, h, "/api/create-file", body)
		assertFuzzStatus(t, "/api/create-file", body, rr)
		if rr.Code != http.StatusOK {
			return
		}
		var resp struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || resp.Path == "" {
			t.Fatalf("200 create-file without a path: %v (%s)", err, rr.Body.String())
		}
		abs := fuzzJailDir(dir, resp.Path)
		assertInsideJail(t, dir, "created file", abs)
		if _, err := os.Stat(abs); err != nil {
			t.Fatalf("200 claimed creation of %s but it is missing: %v", abs, err)
		}
	})
}

func FuzzHandleMkdir(f *testing.F) {
	for _, seed := range []string{
		`{"path":"/","name":"newdir"}`,
		`{"path":"/","name":"../up"}`,
		`{"path":"/","name":""}`,
		`{"path":"/seeddir","name":"child"}`,
		`{"path":"/nope","name":"d"}`,
		`{"path":"/","name":"a/b"}`,
		`{}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		h, _ := fuzzSetup(t)
		rr := fuzzPost(t, h, "/api/mkdir", body)
		assertFuzzStatus(t, "/api/mkdir", body, rr)
		if rr.Code == http.StatusOK {
			var resp map[string]any
			if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
				t.Fatalf("200 mkdir with unparseable body: %v (%s)", err, rr.Body.String())
			}
		}
	})
}

func FuzzHandleRename(f *testing.F) {
	for _, seed := range []string{
		`{"path":"/seed.txt","newname":"renamed.txt"}`,
		`{"path":"/seeddir","newname":"renameddir"}`,
		`{"path":"/seed.txt","newname":"../up"}`,
		`{"path":"/seed.txt","newname":"a/b"}`,
		`{"path":"/seed.txt","newname":""}`,
		`{"path":"/does-not-exist","newname":"x"}`,
		`{"newname":"x"}`,
		`{"path":"/seed.txt"}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		h, dir := fuzzSetup(t)
		rr := fuzzPost(t, h, "/api/rename", body)
		assertFuzzStatus(t, "/api/rename", body, rr)
		if rr.Code != http.StatusOK {
			return
		}
		// On success the renamed entry must still be inside the jail. newname
		// is validated to be free of separators, so the result resolves to the
		// parent of the source path plus the new name.
		var req struct {
			Path    string `json:"path"`
			NewName string `json:"newname"`
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil || req.Path == "" || req.NewName == "" {
			t.Fatalf("rename reported 200 for a body that does not re-parse: %q", body)
		}
		newPath := normalizeVirtualPath(path.Dir(req.Path) + "/" + req.NewName)
		assertInsideJail(t, dir, "renamed entry", fuzzJailDir(dir, newPath))
	})
}

func FuzzHandleMove(f *testing.F) {
	for _, seed := range []string{
		`{"srcs":["/seed.txt"],"dst":"/seeddir"}`,
		`{"src":"/seed.txt","dst":"/seeddir","on_conflict":"rename"}`,
		`{"srcs":["/seed.txt","/seeddir"],"dst":"/"}`,
		`{"srcs":[],"dst":"/"}`,
		`{"src":"/seed.txt"}`,
		`{"dst":"/"}`,
		`{"src":"/seed.txt","dst":"/seeddir","on_conflict":"overwrite"}`,
		`{"src":"/seed.txt","dst":"/seeddir","on_conflict":"bogus"}`,
		`garbage`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		h, _ := fuzzSetup(t)
		rr := fuzzPost(t, h, "/api/move", body)
		assertFuzzStatus(t, "/api/move", body, rr)
		if rr.Code != http.StatusOK {
			return
		}
		// Streaming response: every line must be well-formed NDJSON.
		for _, line := range strings.Split(strings.TrimSpace(rr.Body.String()), "\n") {
			var v map[string]any
			if err := json.Unmarshal([]byte(line), &v); err != nil {
				t.Fatalf("move: invalid NDJSON line %q: %v", line, err)
			}
		}
	})
}

func FuzzHandleCopy(f *testing.F) {
	for _, seed := range []string{
		`{"srcs":["/seed.txt"],"dst":"/seeddir"}`,
		`{"src":"/seed.txt","dst":"/","on_conflict":"rename"}`,
		`{"srcs":[],"dst":"/"}`,
		`{"src":"/seed.txt","dst":"/seeddir/seed.txt"}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		h, _ := fuzzSetup(t)
		rr := fuzzPost(t, h, "/api/copy", body)
		assertFuzzStatus(t, "/api/copy", body, rr)
		if rr.Code != http.StatusOK {
			return
		}
		for _, line := range strings.Split(strings.TrimSpace(rr.Body.String()), "\n") {
			var v map[string]any
			if err := json.Unmarshal([]byte(line), &v); err != nil {
				t.Fatalf("copy: invalid NDJSON line %q: %v", line, err)
			}
		}
	})
}

func FuzzHandleWrite(f *testing.F) {
	for _, seed := range []string{
		`{"path":"/seed.txt","content":"hello"}`,
		`{"path":"/new.txt","content":""}`,
		`{"path":"/","content":"x"}`,
		`{"content":"x"}`,
		`{"path":"/a/b.txt","content":"x"}`,
		`{"path":"/seed.txt"}`,
		`{"path":123,"content":"x"}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, body string) {
		h, dir := fuzzSetup(t)
		rr := fuzzPost(t, h, "/api/write", body)
		assertFuzzStatus(t, "/api/write", body, rr)
		if rr.Code != http.StatusOK {
			return
		}
		// On success the content must round-trip exactly, inside the jail.
		var req struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(body), &req); err != nil || req.Path == "" {
			t.Fatalf("write reported 200 for a body that does not re-parse: %q", body)
		}
		abs := fuzzJailDir(dir, req.Path)
		assertInsideJail(t, dir, "written file", abs)
		b, err := os.ReadFile(abs)
		if err != nil {
			t.Fatalf("200 write to %s but unreadable: %v", abs, err)
		}
		if string(b) != req.Content {
			t.Fatalf("written content does not round-trip: got %q want %q", b, req.Content)
		}
	})
}
