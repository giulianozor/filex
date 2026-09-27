package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestBook builds a small but complete EPUB 2 book with an NCX table of
// contents, a cover and two chapters, and returns its path.
func writeTestBook(t *testing.T, dir, name string) string {
	t.Helper()
	const cover = "\x89PNG\r\n\x1a\nnot really a png"
	container := `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`
	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>A Test Book</dc:title>
    <dc:creator opf:role="aut">A. Author</dc:creator>
    <dc:language>en</dc:language>
    <dc:publisher>A Publisher</dc:publisher>
    <dc:date>2020-01-02</dc:date>
  </metadata>
  <manifest>
    <item id="cover-image" href="cover.png" media-type="image/png" properties="cover-image"/>
    <item id="css" href="style.css" media-type="text/css"/>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
  </manifest>
  <spine toc="ncx">
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
  <guide>
    <reference type="cover-image" href="cover.png" title="Cover"/>
  </guide>
</package>`
	ncx := `<?xml version="1.0"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head><meta name="dtb:uid" content="bookid"/></head>
  <docTitle><text>A Test Book</text></docTitle>
  <navMap>
    <navPoint id="p1" playOrder="1"><navLabel><text>Beginnings</text></navLabel>
      <content src="ch1.xhtml"/></navPoint>
    <navPoint id="p2" playOrder="2"><navLabel><text>Endings</text></navLabel>
      <content src="ch2.xhtml"/></navPoint>
  </navMap>
</ncx>`
	css := `body { color: black; background: white; }`
	ch1 := `<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Beginnings</title><link rel="stylesheet" href="style.css"/></head>
<body>
<script>alert('boom')</script>
<h1 id="top">Beginnings</h1>
<p><img src="cover.png" alt="cover"/></p>
<p><a href="ch2.xhtml">Onwards</a> and <a href="http://example.com/">away</a></p>
</body>
</html>`
	ch2 := `<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml">
<head><title>Endings</title></head>
<body><h1>Endings</h1><p>The end.</p></body>
</html>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct{ name, body string }{
		{"mimetype", "application/epub+zip"},
		{"META-INF/container.xml", container},
		{"OEBPS/content.opf", opf},
		{"OEBPS/toc.ncx", ncx},
		{"OEBPS/style.css", css},
		{"OEBPS/ch1.xhtml", ch1},
		{"OEBPS/ch2.xhtml", ch2},
		{"OEBPS/cover.png", cover},
	} {
		w, err := zw.Create(f.name)
		if err != nil {
			t.Fatalf("zip create %s: %v", f.name, err)
		}
		if _, err := w.Write([]byte(f.body)); err != nil {
			t.Fatalf("zip write %s: %v", f.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write book: %v", err)
	}
	return path
}

// epubResourceRequest asks for one entry of the book at path.
func epubResourceRequest(t *testing.T, h *Handler, path, entry string) *httptest.ResponseRecorder {
	t.Helper()
	q := url.Values{"path": {path}, "entry": {entry}}
	rr := doRequest(t, h, "GET", "/api/epub/resource?"+q.Encode(), nil)
	return rr
}

func TestHandleEpubManifest(t *testing.T) {
	h, dir := setupHandler(t)
	writeTestBook(t, dir, "book.epub")

	rr := doRequest(t, h, "GET", "/api/epub?path="+url.QueryEscape("/book.epub"), nil)
	if rr.Code != 200 {
		t.Fatalf("manifest: status = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	var book epubBookJSON
	if err := json.Unmarshal(rr.Body.Bytes(), &book); err != nil {
		t.Fatalf("decode manifest: %v (%s)", err, rr.Body.String())
	}
	if book.Title != "A Test Book" || book.Author != "A. Author" {
		t.Errorf("metadata = %q by %q, want %q by %q", book.Title, book.Author, "A Test Book", "A. Author")
	}
	if book.Publisher != "A Publisher" || book.Date != "2020-01-02" || book.Language != "en" {
		t.Errorf("metadata publisher/date/language = %q/%q/%q", book.Publisher, book.Date, book.Language)
	}
	if len(book.Docs) != 2 {
		t.Fatalf("got %d docs, want 2", len(book.Docs))
	}
	if book.Docs[0].Path != "OEBPS/ch1.xhtml" || book.Docs[0].Title != "Beginnings" {
		t.Errorf("first doc = %+v, want OEBPS/ch1.xhtml titled Beginnings", book.Docs[0])
	}
	if book.Docs[0].MediaType != "application/xhtml+xml" {
		t.Errorf("first doc media type = %q", book.Docs[0].MediaType)
	}
	// Every document must be addressed by a URL carrying both the book and the
	// entry: each resource is a separate request that is access-checked alone,
	// so the reader has no other way to ask for one.
	if !strings.Contains(book.Docs[0].URL, "path=%2Fbook.epub") ||
		!strings.Contains(book.Docs[0].URL, "entry=OEBPS%2Fch1.xhtml") {
		t.Errorf("doc URL %q does not address the entry", book.Docs[0].URL)
	}
	if !strings.Contains(book.Cover, "entry=OEBPS%2Fcover.png") {
		t.Errorf("cover URL %q does not address the cover", book.Cover)
	}
	if len(book.TOC) != 2 {
		t.Fatalf("got %d TOC entries, want 2: %+v", len(book.TOC), book.TOC)
	}
	if book.TOC[0].Title != "Beginnings" || book.TOC[0].Doc != 0 {
		t.Errorf("first TOC entry = %+v, want Beginnings at doc 0", book.TOC[0])
	}
	if book.TOC[1].Doc != 1 {
		t.Errorf("second TOC entry doc = %d, want 1", book.TOC[1].Doc)
	}
}

func TestHandleEpubResourceRewritesDocument(t *testing.T) {
	h, dir := setupHandler(t)
	writeTestBook(t, dir, "book.epub")

	rr := epubResourceRequest(t, h, "/book.epub", "OEBPS/ch1.xhtml")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	policy := rr.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "img-src 'self'", "form-action 'none'", "base-uri 'none'"} {
		if !strings.Contains(policy, want) {
			t.Errorf("CSP %q missing %q", policy, want)
		}
	}
	if strings.Contains(policy, "script-src") {
		t.Errorf("CSP %q must not permit script", policy)
	}

	body := rr.Body.String()
	if strings.Contains(body, "<script") || strings.Contains(body, "alert(") {
		t.Errorf("script survived rewriting:\n%s", body)
	}
	// In-book references must point back at the resource endpoint, and the
	// external link must be gone rather than rewritten.
	if !strings.Contains(body, "entry=OEBPS%2Fch2.xhtml") {
		t.Errorf("chapter link not rewritten to the endpoint:\n%s", body)
	}
	if strings.Contains(body, "example.com") {
		t.Errorf("external link survived rewriting:\n%s", body)
	}
	// The stylesheet is inlined so a book cannot pull a style from a path the
	// policy would have to widen to allow.
	if !strings.Contains(body, "color: black") {
		t.Errorf("stylesheet not inlined:\n%s", body)
	}
	// The reader's own base rules are present alongside the book's.
	if !strings.Contains(body, "overflow-wrap: break-word") {
		t.Errorf("reader CSS missing:\n%s", body)
	}
}

func TestHandleEpubResourceServesRawEntry(t *testing.T) {
	h, dir := setupHandler(t)
	writeTestBook(t, dir, "book.epub")

	rr := epubResourceRequest(t, h, "/book.epub", "OEBPS/style.css")
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "text/css" {
		t.Errorf("Content-Type = %q, want text/css", got)
	}
	if !strings.Contains(rr.Body.String(), "color: black") {
		t.Errorf("stylesheet body = %q", rr.Body.String())
	}
	// A raw resource is fetched by a document that already carries the
	// policy, so it must not repeat it.
	if policy := rr.Header().Get("Content-Security-Policy"); policy != "" {
		t.Errorf("raw resource carries CSP %q", policy)
	}
}

func TestHandleEpubResourceRejectsUnknownEntry(t *testing.T) {
	h, dir := setupHandler(t)
	writeTestBook(t, dir, "book.epub")

	// An entry that is not in the book, and attempts to escape it, must all be
	// plain 404s: this endpoint can only ever name bytes inside the archive.
	for _, entry := range []string{
		"OEBPS/nope.xhtml",
		"../../etc/passwd",
		"/etc/passwd",
		"OEBPS/../OEBPS/ch1.xhtml",
	} {
		if rr := epubResourceRequest(t, h, "/book.epub", entry); rr.Code != 404 {
			t.Errorf("entry %q: status = %d, want 404 (%s)", entry, rr.Code, rr.Body.String())
		}
	}
}

func TestHandleEpubRejectsNonBook(t *testing.T) {
	h, dir := setupHandler(t)
	if err := os.WriteFile(filepath.Join(dir, "plain.epub"), []byte("not a zip at all"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "dir.epub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	for _, path := range []string{"/plain.epub", "/dir.epub"} {
		rr := doRequest(t, h, "GET", "/api/epub?path="+url.QueryEscape(path), nil)
		if rr.Code != 422 {
			t.Errorf("path %s: status = %d, want 422 (%s)", path, rr.Code, rr.Body.String())
		}
	}
	rr := doRequest(t, h, "GET", "/api/epub?path="+url.QueryEscape("/missing.epub"), nil)
	if rr.Code != 404 {
		t.Errorf("missing book: status = %d, want 404", rr.Code)
	}
}

func TestHandleEpubRequiresPathEntryAndGet(t *testing.T) {
	h, dir := setupHandler(t)
	writeTestBook(t, dir, "book.epub")

	if rr := doRequest(t, h, "GET", "/api/epub", nil); rr.Code != 400 {
		t.Errorf("manifest without path: status = %d, want 400", rr.Code)
	}
	if rr := epubResourceRequest(t, h, "/book.epub", ""); rr.Code != 400 {
		t.Errorf("resource without entry: status = %d, want 400", rr.Code)
	}
	// Both endpoints only read. A write must not slip through to the read path.
	for _, target := range []string{
		"/api/epub?path=/book.epub",
		"/api/epub/resource?path=/book.epub&entry=OEBPS%2Fch1.xhtml",
	} {
		if rr := doRequest(t, h, "POST", target, strings.NewReader("{}")); rr.Code == 200 {
			t.Errorf("POST %s was accepted", target)
		}
	}
}
