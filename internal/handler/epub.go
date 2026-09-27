package handler

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/giulianozor/filex/internal/epub"
)

// epubReaderCSS is prepended to every rendered content document. It stays
// deliberately small: a book brings its own typography, and the reader only
// fixes what would otherwise spill out of the reading column.
const epubReaderCSS = `
html { -webkit-text-size-adjust: 100%; }
body { overflow-wrap: break-word; word-wrap: break-word; }
img, svg, video, audio, canvas { max-width: 100%; }
img, video, canvas { height: auto; }
table { max-width: 100%; }
`

// epubContentPolicy is sent with every rendered content document and is the
// boundary that keeps a book on screen as text. Book content is untrusted
// input: it may not run script, frame anything, submit a form, or reach the
// network except through the resource endpoint on this same origin — which can
// only ever hand back bytes from inside this book. 'unsafe-inline' is allowed
// for styles because a book's stylesheets are served inline (a separate
// stylesheet response would need a broader policy), and because the reader
// injects its own theme the same way.
//
// There is deliberately no CSP "sandbox" directive: the reader sizes the frame
// to its content and restyles it, both of which need same-origin access to the
// document, and the policy above already denies the document every capability
// script would need to abuse that access.
const epubContentPolicy = "default-src 'none'; " +
	"style-src 'unsafe-inline' 'self'; " +
	"img-src 'self' data:; " +
	"font-src 'self' data:; " +
	"media-src 'self' data:; " +
	"form-action 'none'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'self'"

// epubDocJSON is one reading-order entry as the reader consumes it.
type epubDocJSON struct {
	// Path is the entry's name inside the archive. The reader matches it
	// against in-document links to decide which chapter a click should open.
	Path string `json:"path"`
	// Title is a label for the chapter picker and the table of contents.
	Title string `json:"title"`
	// MediaType is the type the package document declared.
	MediaType string `json:"media_type"`
	// URL is where the document itself is served from.
	URL string `json:"url"`
}

// epubNavJSON is one table-of-contents entry. Doc is an index into the book's
// docs, or -1 for a heading that only groups its children or points somewhere
// outside the reading order.
type epubNavJSON struct {
	Title    string        `json:"title"`
	Doc      int           `json:"doc"`
	Fragment string        `json:"fragment,omitempty"`
	Children []epubNavJSON `json:"children,omitempty"`
}

// epubBookJSON is everything the reader needs to render a book: what it is
// called, where each chapter is served from, and how the chapters nest.
type epubBookJSON struct {
	Title     string        `json:"title"`
	Author    string        `json:"author"`
	Language  string        `json:"language"`
	Publisher string        `json:"publisher"`
	Date      string        `json:"date"`
	Cover     string        `json:"cover,omitempty"`
	Docs      []epubDocJSON `json:"docs"`
	TOC       []epubNavJSON `json:"toc"`
}

// handleEpub serves a book's manifest: its metadata, its reading order and its
// table of contents, with every entry addressed by a URL the reader can fetch.
func (h *Handler) handleEpub(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	path := requireQueryPath(w, r)
	if path == "" {
		return
	}
	book, closer, ok := h.openEpub(w, r, path)
	if !ok {
		return
	}
	defer closer()
	writeJSON(w, epubManifestFor(book, path))
}

// handleEpubResource serves one entry of a book: a content document, rewritten
// and pinned with a content policy, or any other file (image, font, audio)
// served as the type the package document declared. Nothing is ever read from
// outside the book, so the request cannot be used to fetch arbitrary files.
func (h *Handler) handleEpubResource(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	path := requireQueryPath(w, r)
	if path == "" {
		return
	}
	entry := r.URL.Query().Get("entry")
	if entry == "" {
		writeError(w, http.StatusBadRequest, "entry required")
		return
	}

	book, closer, ok := h.openEpub(w, r, path)
	if !ok {
		return
	}
	defer closer()

	// The book is re-parsed per request, so each resource is access-checked
	// from scratch and a permission change takes effect immediately.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if book.IsDocument(entry) {
		data, err := book.Document(entry, epubResourceURLFor(path), epubReaderCSS)
		if err != nil {
			writeEpubError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", epubContentPolicy)
		_, _ = w.Write(data)
		return
	}

	data, mediaType, err := book.Resource(entry)
	if err != nil {
		writeEpubError(w, err)
		return
	}
	w.Header().Set("Content-Type", mediaType)
	_, _ = w.Write(data)
}

// openEpub opens the book at the request's path and parses it. It returns the
// book, a closer the caller must run once the response is written, and whether
// the caller may proceed (false means an error response has been sent).
//
// A Book reads through the open file, so the file has to stay readable until
// the response is finished with it; that is what the closer is for.
func (h *Handler) openEpub(w http.ResponseWriter, r *http.Request, path string) (*epub.Book, func(), bool) {
	// A NUL in the path is rejected by the kernel as an invalid argument, which
	// would otherwise be reported as a server fault. It is a malformed request.
	if strings.IndexByte(path, 0) >= 0 {
		writeError(w, http.StatusBadRequest, "invalid path")
		return nil, nil, false
	}
	file, err := h.fsForRequest(r).OpenForDownload(path)
	if err != nil {
		// Any failure to open the path the caller named means there is no book
		// there: a missing file, a path outside the jail, or a name the OS
		// rejects outright. The download endpoint reports the same failures the
		// same way; only the permission case is worth calling out separately.
		if errors.Is(err, os.ErrPermission) {
			writeError(w, http.StatusForbidden, err.Error())
		} else {
			writeError(w, http.StatusNotFound, err.Error())
		}
		return nil, nil, false
	}
	book, err := parseEpubFile(file)
	if err != nil {
		file.Close()
		writeEpubError(w, err)
		return nil, nil, false
	}
	return book, func() { _ = file.Close() }, true
}

// errNotABook marks a path that cannot be a book at all, so it is reported as
// the caller's problem with their file (422) rather than as a server fault.
var errNotABook = errors.New("not a book: path is a directory")

// parseEpubFile stats and parses an opened book, rejecting a directory before
// the archive reader sees it.
func parseEpubFile(file *os.File) (*epub.Book, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errNotABook
	}
	return epub.Open(file, info.Size())
}

// writeEpubError maps a book-parsing failure onto an HTTP status. A file that
// is not a readable book is the caller's problem with their file (422), not a
// server fault, so it is reported as such instead of as a 500.
func writeEpubError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotABook),
		errors.Is(err, epub.ErrNotArchive),
		errors.Is(err, epub.ErrNoPackage),
		errors.Is(err, epub.ErrNoSpine):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, epub.ErrNotFound), errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, epub.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// epubManifestFor turns a parsed book into the reader's payload, addressing
// every document and the cover by URL and pointing table-of-contents entries at
// an index into Docs, so the client never has to resolve an archive path
// itself.
func epubManifestFor(book *epub.Book, path string) epubBookJSON {
	byPath := make(map[string]int, len(book.Docs))
	for i, d := range book.Docs {
		byPath[d.Href] = i
	}
	// The table of contents carries the only real chapter names in most books,
	// so it wins over the file name the parser could derive on its own. The
	// first entry for a document is its title; later ones are sections within
	// it, which the reader can still reach through their own links.
	titles := epubTitlesFromTOC(book.TOC, byPath)
	docs := make([]epubDocJSON, 0, len(book.Docs))
	for i, d := range book.Docs {
		title := d.Title
		if t := titles[i]; t != "" {
			title = t
		}
		docs = append(docs, epubDocJSON{
			Path:      d.Href,
			Title:     title,
			MediaType: d.MediaType,
			URL:       epubResourceURLFor(path)(d.Href),
		})
	}
	out := epubBookJSON{
		Title:     book.Title,
		Author:    book.Author,
		Language:  book.Language,
		Publisher: book.Publisher,
		Date:      book.Date,
		Docs:      docs,
		TOC:       epubNavItems(book.TOC, byPath),
	}
	if book.Cover != "" {
		out.Cover = epubResourceURLFor(path)(book.Cover)
	}
	return out
}

// epubTitlesFromTOC maps a reading-order index to the first title the table of
// contents gives it.
func epubTitlesFromTOC(items []epub.NavItem, byPath map[string]int) map[int]string {
	titles := make(map[int]string, len(byPath))
	var walk func(items []epub.NavItem)
	walk = func(items []epub.NavItem) {
		for _, it := range items {
			if doc, ok := byPath[it.Href]; ok && titles[doc] == "" && it.Title != "" {
				titles[doc] = it.Title
			}
			if len(it.Children) > 0 {
				walk(it.Children)
			}
		}
	}
	walk(items)
	return titles
}

// epubNavItems flattens the table of contents into the client shape, resolving
// each entry's target to a reading-order index.
func epubNavItems(items []epub.NavItem, byPath map[string]int) []epubNavJSON {
	out := make([]epubNavJSON, 0, len(items))
	for _, it := range items {
		doc, ok := byPath[it.Href]
		if !ok {
			doc = -1
		}
		entry := epubNavJSON{Title: it.Title, Doc: doc, Fragment: it.Fragment}
		if len(it.Children) > 0 {
			entry.Children = epubNavItems(it.Children, byPath)
		}
		out = append(out, entry)
	}
	return out
}

// epubResourceURLFor builds the URL one entry of a book is served from. The
// book's own path travels with every resource URL because each resource is a
// separate request that has to be access-checked on its own.
func epubResourceURLFor(path string) epub.URLFunc {
	return func(entry string) string {
		q := url.Values{"path": []string{path}, "entry": []string{entry}}
		return "/api/epub/resource?" + q.Encode()
	}
}
