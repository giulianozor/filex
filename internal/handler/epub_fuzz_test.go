package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The EPUB read endpoints take a path to a book and a second parameter naming an
// entry inside that book. The second parameter is the interesting one: it names
// a file within an archive, so a bug in it would be a read of anything the
// server process can open. This target fuzzes both parameters together.
//
// The invariants enforced per exec:
//
//   - the handler never panics (a recovered panic surfaces as a 500 and fails
//     the allowlist below) and never leaks an unexpected status code;
//   - a 200 response never contains a canary planted outside the book, so no
//     input can steer the endpoint at a file on disk;
//   - a 200 content document always arrives with the policy and the nosniff
//     header that make it inert in the reader's frame.

// epubCanary is the content of a file outside the book. Any appearance of it in
// a response means the endpoint read something it should not have.
const epubCanary = "CANARY-f8a2c1d4-not-a-book-member"

// fuzzEpubSetup builds a handler whose temp dir holds a real book, a plain file
// and a directory that all look like plausible targets, plus the canary.
func fuzzEpubSetup(t *testing.T) (*Handler, string) {
	t.Helper()
	h, dir := setupHandler(t)
	writeTestBook(t, dir, "book.epub")
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte(epubCanary), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "outside", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outside", "deep", "secret.txt"), []byte(epubCanary), 0o644); err != nil {
		t.Fatal(err)
	}
	return h, dir
}

// assertEpubStatus fails when the response status is not one of the statuses
// these read handlers are allowed to produce. A 500 in particular means either
// a recovered panic or an error path that was never expected.
func assertEpubStatus(t *testing.T, query string, rr *httptest.ResponseRecorder) {
	t.Helper()
	switch rr.Code {
	case http.StatusOK, http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound,
		http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		// 200 = a book (or entry) was read, the rest are the ways a request can
		// be refused: no path, no entry, no permission, no such book, too big,
		// or a file that is not a readable book.
	default:
		t.Fatalf("status %d for query %q: %s", rr.Code, query, rr.Body.String())
	}
}

func FuzzHandleEpubResource(f *testing.F) {
	for _, query := range []string{
		"path=/book.epub&entry=OEBPS%2Fch1.xhtml",
		"path=/book.epub&entry=OEBPS%2Fch1.xhtml&entry=OEBPS%2Fch2.xhtml",
		"path=/book.epub&entry=OEBPS%2Fcover.png",
		"path=/book.epub&entry=cover.png",
		"path=/book.epub&entry=",
		"path=/book.epub",
		"entry=OEBPS%2Fch1.xhtml",
		"path=&entry=",
		"path=/secret.txt&entry=OEBPS%2Fch1.xhtml",
		"path=/secret.txt&entry=secret.txt",
		"path=/outside&entry=deep%2Fsecret.txt",
		"path=/outside%2Fdeep%2Fsecret.txt&entry=secret.txt",
		"path=/book.epub&entry=..%2F..%2Fetc%2Fpasswd",
		"path=/book.epub&entry=%2Fetc%2Fpasswd",
		"path=/book.epub&entry=OEBPS%2F..%2F..%2Fsecret.txt",
		"path=/book.epub&entry=OEBPS%2F%2F%2Fsecret.txt",
		"path=/book.epub&entry=OEBPS%2Fch1.xhtml%23frag",
		"path=/book.epub&entry=OEBPS%2Fch1.xhtml&path=/secret.txt",
		"path=..%2F..%2Fetc&entry=passwd",
		"path=%2Fbook.epub&entry=%00",
		// Two paths the kernel rejects outright rather than resolving: a NUL in
		// the path, and a single component longer than NAME_MAX. This fuzzer
		// found both, and each used to surface as a 500.
		"path=%00&entry=0",
		"garbage",
		"",
		"&&&",
	} {
		f.Add(query)
	}
	// A component longer than NAME_MAX fails with ENAMETOOLONG, like the NUL
	// above; it needs a computed seed because the literal is unreadable.
	f.Add("path=/" + strings.Repeat("x", 300) + ".epub")
	f.Fuzz(func(t *testing.T, query string) {
		h, _ := fuzzEpubSetup(t)
		// Build the request around a fixed path and set the fuzzed text as the
		// raw query, so a malformed query cannot panic request construction.
		req := httptest.NewRequest(http.MethodGet, "/api/epub/resource", nil)
		req.URL.RawQuery = query
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		assertEpubStatus(t, query, rr)
		if body := rr.Body.String(); strings.Contains(body, epubCanary) {
			t.Fatalf("query %q returned content from outside the book: %q", query, body)
		}
		if rr.Code != http.StatusOK {
			return
		}
		// A content document is the only response the reader renders, so it
		// must always be pinned to the policy that keeps it inert.
		if rr.Header().Get("Content-Type") == "text/html; charset=utf-8" {
			if rr.Header().Get("Content-Security-Policy") == "" {
				t.Fatalf("query %q: content document without a content policy", query)
			}
			if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("query %q: content document without nosniff", query)
			}
		}
	})
}
