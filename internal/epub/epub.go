// Package epub reads EPUB 2 and EPUB 3 books out of a ZIP archive and prepares
// their content documents for display in a web view.
//
// A book is described by two XML documents: the OCF container
// (META-INF/container.xml) points at the package document (OPF), which declares
// the manifest of every file in the archive and the spine that fixes the
// reading order. Nothing is ever written to disk — entries are served straight
// out of the archive, and every content document is rewritten so that the URLs
// it references point back at the archive through a caller-supplied URL
// builder. That keeps a whole book behind one access-checked endpoint and
// leaves the caller free to decide what a resource URL looks like.
package epub

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"path"
	"strings"
)

// Errors reported for a file that cannot be read as a book. Callers map these
// onto HTTP status codes, so they are compared with errors.Is.
var (
	// ErrNotArchive means the file is not a ZIP archive at all.
	ErrNotArchive = errors.New("not a ZIP archive")
	// ErrNoPackage means the OCF container or the package document it points
	// at is missing or unparsable.
	ErrNoPackage = errors.New("no package document (META-INF/container.xml is missing or invalid)")
	// ErrNoSpine means the package document lists no reading order, so there
	// is nothing to show.
	ErrNoSpine = errors.New("no reading order (the package document declares no spine items)")
	// ErrNotFound means the requested entry is not part of the book.
	ErrNotFound = errors.New("not part of this book")
	// ErrTooLarge means an entry exceeds the size this package will
	// decompress, either on its own or across the book as a whole.
	ErrTooLarge = errors.New("too large to read")
)

// Size bounds. Every read is limited, and the sum of all reads from one archive
// is limited as well, so a small file that expands to gigabytes (a "zip bomb")
// cannot exhaust memory.
const (
	// maxSmallDocBytes caps container.xml, the package document, the NCX and a
	// navigation document — all of which are a few kilobytes in practice.
	maxSmallDocBytes = 8 << 20
	// maxResourceBytes caps a single non-document entry (stylesheet, image,
	// font or audio track) served to the reader.
	maxResourceBytes = 32 << 20
	// maxDocumentBytes caps one content document before rewriting. The
	// rewritten form is larger because of the inlined stylesheets.
	maxDocumentBytes = 32 << 20
	// maxBookBytes caps everything decompressed out of one archive.
	maxBookBytes = 512 << 20
)

// Doc is one document of the reading order.
type Doc struct {
	// Href is the entry's path inside the archive, e.g. "OEBPS/ch1.xhtml".
	Href string
	// Title is a best-effort label: the manifest rarely carries one, so it
	// falls back to the file name.
	Title string
	// MediaType is the type the package document declares for the entry.
	MediaType string
}

// NavItem is one table-of-contents entry. Children holds the entries nested
// under it, and Href is empty for a heading that only groups its children.
// Fragment addresses a position inside Href and is empty for a plain chapter
// entry.
type NavItem struct {
	Title    string
	Href     string
	Fragment string
	Children []NavItem
}

// URLFunc maps an archive path to the URL a browser should fetch it from.
type URLFunc func(href string) string

// Book is a parsed EPUB. It holds the archive it was read from, so the
// io.ReaderAt passed to Open must stay readable for as long as the Book is
// used. A Book is not safe for concurrent use.
type Book struct {
	// Metadata, all optional: plenty of books omit some or all of it.
	Title     string
	Author    string
	Language  string
	Publisher string
	Date      string
	// Cover is the archive path of the cover image, empty when the book has
	// none or does not say which image it is.
	Cover string
	// Docs is the reading order. It is never empty for a book that opened.
	Docs []Doc
	// TOC is the table of contents, which may be empty for a book that
	// carries no navigation file.
	TOC []NavItem

	zr     *zip.Reader
	byName map[string]*zip.File
	byFold map[string]*zip.File
	media  map[string]string
	budget int64
}

// Open parses the EPUB held in r, which must be size bytes long.
func Open(r io.ReaderAt, size int64) (*Book, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotArchive, err)
	}
	b := &Book{
		zr:     zr,
		byName: make(map[string]*zip.File, len(zr.File)),
		byFold: make(map[string]*zip.File, len(zr.File)),
		media:  make(map[string]string, len(zr.File)),
		budget: maxBookBytes,
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.TrimPrefix(f.Name, "/")
		if _, seen := b.byName[name]; !seen {
			b.byName[name] = f
		}
		fold := strings.ToLower(name)
		if _, seen := b.byFold[fold]; !seen {
			b.byFold[fold] = f
		}
	}
	if err := b.parse(); err != nil {
		return nil, err
	}
	return b, nil
}

// lookup finds an archive entry by name, falling back to a case-insensitive
// match: real-world books are rarely consistent about the case of the paths
// their documents refer to.
func (b *Book) lookup(name string) (*zip.File, bool) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "/")
	if name == "" {
		return nil, false
	}
	if f, ok := b.byName[name]; ok {
		return f, true
	}
	f, ok := b.byFold[strings.ToLower(name)]
	return f, ok
}

// read decompresses one entry, bounded both by limit and by the archive-wide
// budget. The declared uncompressed size is checked first so a header that
// understates the payload cannot make the reader allocate past the limit.
func (b *Book) read(f *zip.File, limit int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("%w: %s declares %d bytes", ErrTooLarge, f.Name, f.UncompressedSize64)
	}
	if b.budget <= 0 {
		return nil, fmt.Errorf("%w: the book expands past %d bytes", ErrTooLarge, maxBookBytes)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	bounded := min(limit, b.budget)
	data, err := io.ReadAll(io.LimitReader(rc, bounded+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > bounded {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes", ErrTooLarge, f.Name, bounded)
	}
	b.budget -= int64(len(data))
	return data, nil
}

// parse locates and reads the package document, then fills in everything the
// reader needs from it.
func (b *Book) parse() error {
	opfPath, err := b.packagePath()
	if err != nil {
		return err
	}
	opfFile, ok := b.lookup(opfPath)
	if !ok {
		return fmt.Errorf("%w: %s is missing", ErrNoPackage, opfPath)
	}
	opfData, err := b.read(opfFile, maxSmallDocBytes)
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			return fmt.Errorf("%w: the package document %s", ErrTooLarge, opfPath)
		}
		return fmt.Errorf("%w: %v", ErrNoPackage, err)
	}
	var pkg packageDocument
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return fmt.Errorf("%w: %v", ErrNoPackage, err)
	}
	return b.applyPackage(&pkg, path.Dir(opfPath))
}

// packagePath reads META-INF/container.xml and returns the path it gives for
// the package document.
func (b *Book) packagePath() (string, error) {
	container, ok := b.lookup("META-INF/container.xml")
	if !ok {
		return "", ErrNoPackage
	}
	data, err := b.read(container, maxSmallDocBytes)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoPackage, err)
	}
	var c struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoPackage, err)
	}
	for _, rf := range c.Rootfiles {
		if p := strings.TrimSpace(rf.FullPath); p != "" {
			return p, nil
		}
	}
	return "", ErrNoPackage
}

// applyPackage fills the book from a parsed package document. base is the
// directory the package document lives in: every href in it is relative to
// there, not to the archive root.
func (b *Book) applyPackage(pkg *packageDocument, base string) error {
	items := make(map[string]manifestRef, len(pkg.Manifest.Items))
	for _, it := range pkg.Manifest.Items {
		href := resolveRef(base, it.Href)
		if href == "" || strings.TrimSpace(it.ID) == "" {
			continue
		}
		ref := manifestRef{href: href, mediaType: strings.TrimSpace(it.MediaType)}
		items[strings.TrimSpace(it.ID)] = ref
		if _, ok := b.lookup(href); ok {
			b.media[href] = ref.mediaType
		}
	}

	for _, ref := range pkg.Spine.Itemrefs {
		it, ok := items[strings.TrimSpace(ref.IDRef)]
		if !ok {
			continue
		}
		if _, present := b.lookup(it.href); !present {
			continue
		}
		b.Docs = append(b.Docs, Doc{
			Href:      it.href,
			Title:     humanizeHref(it.href),
			MediaType: b.mediaType(it.href),
		})
	}
	if len(b.Docs) == 0 {
		return ErrNoSpine
	}

	b.Title = firstOf(pkg.Metadata.Titles...)
	b.Author = strings.Join(nonEmpty(pkg.Metadata.Creators...), ", ")
	b.Language = strings.TrimSpace(pkg.Metadata.Language)
	b.Publisher = strings.TrimSpace(pkg.Metadata.Publisher)
	b.Date = strings.TrimSpace(pkg.Metadata.Date)
	b.Cover = b.coverPath(pkg, base, items)
	b.TOC = b.toc(pkg, base, items)
	return nil
}

// coverPath finds the cover image. EPUB 3 marks it with a "cover-image"
// manifest property, EPUB 2 names the manifest id in a <meta name="cover">
// element, and the OCF <guide> cover reference is the last resort. All three
// spellings are in the wild, often within the same file.
func (b *Book) coverPath(pkg *packageDocument, base string, items map[string]manifestRef) string {
	for _, it := range pkg.Manifest.Items {
		if hasProperty(it.Properties, "cover-image") {
			if href := resolveRef(base, it.Href); href != "" {
				return href
			}
		}
	}
	for _, m := range pkg.Metadata.Metas {
		if !strings.EqualFold(strings.TrimSpace(m.Name), "cover") {
			continue
		}
		if it, ok := items[strings.TrimSpace(m.Content)]; ok {
			return it.href
		}
	}
	for _, g := range pkg.Guide.References {
		if strings.EqualFold(strings.TrimSpace(g.Type), "cover") {
			if href := resolveRef(base, g.Href); href != "" {
				return href
			}
		}
	}
	return ""
}

// toc builds the table of contents. An EPUB 2 navigation file (NCX) is
// preferred over an EPUB 3 navigation document because it records the section
// hierarchy explicitly, whereas a nav document has to be walked heuristically.
// A book with neither falls back to its spine.
func (b *Book) toc(pkg *packageDocument, base string, items map[string]manifestRef) []NavItem {
	if id := strings.TrimSpace(pkg.Spine.TOC); id != "" {
		if it, ok := items[id]; ok {
			if toc, err := b.ncxTOC(it.href); err == nil && len(toc) > 0 {
				return toc
			}
		}
	}
	for _, it := range pkg.Manifest.Items {
		if !hasProperty(it.Properties, "nav") {
			continue
		}
		href := resolveRef(base, it.Href)
		if href == "" {
			continue
		}
		if toc, err := b.navDocTOC(href); err == nil && len(toc) > 0 {
			return toc
		}
	}
	return b.spineTOC()
}

type navPoint struct {
	Label struct {
		Text string `xml:"text"`
	} `xml:"navLabel"`
	// Content is the <content src="..."/> element that names the point's
	// target, not an attribute of the navPoint itself.
	Content struct {
		Src string `xml:"src,attr"`
	} `xml:"content"`
	Kids []navPoint `xml:"navPoint"`
}

func (b *Book) ncxTOC(href string) ([]NavItem, error) {
	data, err := b.readEntry(href, maxSmallDocBytes)
	if err != nil {
		return nil, err
	}
	var ncx struct {
		Points []navPoint `xml:"navMap>navPoint"`
	}
	if err := xml.Unmarshal(data, &ncx); err != nil {
		return nil, err
	}
	if len(ncx.Points) == 0 {
		return nil, nil
	}
	// NCX content references are relative to the NCX's own directory, which is
	// not necessarily the package document's.
	return convertNavPoints(ncx.Points, path.Dir(href)), nil
}

func convertNavPoints(points []navPoint, base string) []NavItem {
	out := make([]NavItem, 0, len(points))
	for _, p := range points {
		item := NavItem{Title: collapseSpace(p.Label.Text)}
		item.Href, item.Fragment = navTarget(base, p.Content.Src)
		if len(p.Kids) > 0 {
			item.Children = convertNavPoints(p.Kids, base)
		}
		out = append(out, item)
	}
	return out
}

func (b *Book) navDocTOC(href string) ([]NavItem, error) {
	data, err := b.readEntry(href, maxSmallDocBytes)
	if err != nil {
		return nil, err
	}
	anchors := collectNavAnchors(string(data))
	out := make([]NavItem, 0, len(anchors))
	for _, a := range anchors {
		item := NavItem{Title: a.Title}
		item.Href, item.Fragment = navTarget(path.Dir(href), a.Href)
		out = append(out, item)
	}
	return out, nil
}

// spineTOC synthesises a table of contents from the reading order for books
// that ship no navigation file, so the reader always has a chapter list.
func (b *Book) spineTOC() []NavItem {
	out := make([]NavItem, 0, len(b.Docs))
	for _, d := range b.Docs {
		out = append(out, NavItem{Title: d.Title, Href: d.Href})
	}
	return out
}

// readEntry returns the raw bytes of an archive entry.
func (b *Book) readEntry(href string, limit int64) ([]byte, error) {
	f, ok := b.lookup(href)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, href)
	}
	return b.read(f, limit)
}

// Document returns a content document rewritten for display: every URL it
// references is replaced by urlFor, linked stylesheets are inlined, and
// script, frames, plugins and meta-refresh are removed. injectedCSS is added
// to the document's own styling, and is how the reader applies its baseline
// typography without fighting the book's layout.
func (b *Book) Document(href string, urlFor URLFunc, injectedCSS string) ([]byte, error) {
	src, err := b.readEntry(href, maxDocumentBytes)
	if err != nil {
		return nil, err
	}
	base := path.Dir(href)
	rewrite := b.referenceRewriter(base, urlFor)
	return []byte(rewriteContent(string(src), injectedCSS, rewrite, b.styler(base, urlFor))), nil
}

// styler returns the callback that inlines a stylesheet referenced by a
// document stored in base. The stylesheet is rewritten against its own
// directory, so its relative URLs keep pointing at the right neighbours.
func (b *Book) styler(base string, urlFor URLFunc) styler {
	return func(ref reference) string {
		target, ok := b.localTarget(base, ref)
		if !ok {
			return ""
		}
		css, err := b.readEntry(target, maxResourceBytes)
		if err != nil {
			return ""
		}
		return rewriteCSS(string(css), b.referenceRewriter(path.Dir(target), urlFor))
	}
}

// IsDocument reports whether href addresses markup that has to be rewritten and
// pinned with a content policy before it is served. It is true for the reading
// order and for any other HTML or XHTML entry, which a chapter may well link
// to.
func (b *Book) IsDocument(href string) bool {
	f, ok := b.lookup(href)
	return ok && isHTMLType(b.mediaType(f.Name))
}

// Resource returns the raw bytes and media type of an entry that is not a
// content document (image, stylesheet, font, audio track). HTML and XHTML
// entries are refused: they may only be served through Document, which
// sanitises them and pins them with a Content-Security-Policy, so a book
// cannot smuggle a live page past the reader.
func (b *Book) Resource(href string) (data []byte, mediaType string, err error) {
	f, ok := b.lookup(href)
	if !ok {
		return nil, "", fmt.Errorf("%w: %s", ErrNotFound, href)
	}
	mt := b.mediaType(f.Name)
	if isHTMLType(mt) {
		return nil, "", fmt.Errorf("%w: %s is a content document", ErrNotFound, href)
	}
	data, err = b.read(f, maxResourceBytes)
	if err != nil {
		return nil, "", err
	}
	return data, mt, nil
}

// referenceRewriter maps a reference found in a document stored in base to the
// URL that serves it. It returns "" for references that must be dropped:
// absolute URLs (an external image would phone home and a remote script is not
// ours to run) and paths that climb out of the archive or are not in the book.
func (b *Book) referenceRewriter(base string, urlFor URLFunc) func(reference) string {
	return func(ref reference) string {
		target, ok := b.localTarget(base, ref)
		if !ok {
			return ""
		}
		return urlFor(target)
	}
}

// localTarget resolves a reference against the directory of the document it
// came from, reporting false when it does not address a file in the archive.
func (b *Book) localTarget(base string, ref reference) (string, bool) {
	if ref.Path == "" || ref.External {
		return "", false
	}
	target := resolveRef(base, ref.Path)
	if target == "" {
		return "", false
	}
	if _, ok := b.lookup(target); !ok {
		return "", false
	}
	return target, true
}

// mediaType reports the type of an archive entry: the one the package document
// declares, else one derived from the file extension.
func (b *Book) mediaType(name string) string {
	if mt, ok := b.media[strings.TrimPrefix(name, "/")]; ok && mt != "" {
		return mt
	}
	if mt := mime.TypeByExtension(path.Ext(name)); mt != "" {
		// Drop parameters such as "; charset=utf-8": the reader always serves
		// the media type alone and lets the browser default the encoding.
		if base, _, found := strings.Cut(mt, ";"); found {
			return base
		}
		return mt
	}
	return "application/octet-stream"
}

// ---- package document schema -------------------------------------------------

// packageDocument mirrors the subset of the OPF this package needs. The
// encoding/xml tags deliberately name only the local element name, so both the
// EPUB 2 (dc:title inside metadata) and EPUB 3 (a namespaced title) spellings
// of every field match without a namespace declaration.
type packageDocument struct {
	Metadata packageMetadata `xml:"metadata"`
	Manifest struct {
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		TOC      string `xml:"toc,attr"`
		Itemrefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
	Guide struct {
		References []struct {
			Type string `xml:"type,attr"`
			Href string `xml:"href,attr"`
		} `xml:"reference"`
	} `xml:"guide"`
}

// packageMetadata is the <metadata> block of a package document.
type packageMetadata struct {
	Titles    []string `xml:"title"`
	Creators  []string `xml:"creator"`
	Language  string   `xml:"language"`
	Publisher string   `xml:"publisher"`
	Date      string   `xml:"date"`
	Metas     []struct {
		Name    string `xml:"name,attr"`
		Content string `xml:"content,attr"`
	} `xml:"meta"`
}

// manifestRef is the resolved form of one manifest item.
type manifestRef struct {
	href      string
	mediaType string
}

// ---- small helpers -----------------------------------------------------------

// resolveRef turns a reference stored in a document at base into the archive
// path it points at, percent-decoding it and dropping any query or fragment.
// It returns "" when the reference does not address a file inside the archive:
// a bare fragment, an absolute or protocol-relative URL, or a path that climbs
// above the archive root.
func resolveRef(base, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "//") || hasScheme(ref) {
		return ""
	}
	if i := strings.IndexAny(ref, "#?"); i >= 0 {
		ref = ref[:i]
	}
	if ref == "" {
		return "" // a bare fragment or a bare query: nothing to resolve
	}
	if dec, err := url.PathUnescape(ref); err == nil {
		ref = dec
	}
	// A NUL byte in a name would truncate the path in the C library and is
	// never legitimate; drop the whole reference instead of truncating it.
	if strings.ContainsRune(ref, 0) {
		return ""
	}
	if climbsOut(base, ref) {
		return ""
	}
	joined := path.Join(base, ref)
	if joined == "." || joined == ".." || strings.HasPrefix(joined, "../") {
		return ""
	}
	return joined
}

// climbsOut reports whether resolving ref against base would leave the archive
// root. The check walks the ".." segments by hand because path.Join absorbs
// them silently, which would turn "../../etc/passwd" into a plausible-looking
// archive path instead of a rejection.
func climbsOut(base, ref string) bool {
	depth := 0
	if base != "" && base != "." {
		depth = len(strings.Split(base, "/"))
	}
	for _, seg := range strings.Split(ref, "/") {
		switch seg {
		case "", ".":
		case "..":
			if depth == 0 {
				return true
			}
			depth--
		default:
			depth++
		}
	}
	return false
}

// hasScheme reports whether ref starts with a URL scheme, so that "http:",
// "data:" and "mailto:" are recognised as absolute and left alone rather than
// being resolved against the book. A leading letter is required, which is what
// separates a scheme from an ordinary relative file name.
func hasScheme(ref string) bool {
	for i := 0; i < len(ref); i++ {
		c := ref[i]
		if c == ':' {
			return i > 0
		}
		if i == 0 {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
				return false
			}
			continue
		}
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '+' || c == '-' || c == '.':
		default:
			return false
		}
	}
	return false
}

// navTarget resolves a navigation reference into the archive path it opens and
// the fragment it points at within it. A reference to a position in the current
// document keeps an empty path and a fragment, so the reader can scroll to it
// instead of reloading the chapter.
func navTarget(base, ref string) (href, fragment string) {
	s := splitReference(ref)
	return resolveRef(base, s.Path), s.Fragment
}

// hasProperty reports whether a manifest item's space-separated properties list
// contains want.
func hasProperty(properties, want string) bool {
	for _, p := range strings.Fields(properties) {
		if p == want {
			return true
		}
	}
	return false
}

func isHTMLType(mediaType string) bool {
	mt := strings.ToLower(strings.TrimSpace(mediaType))
	return mt == "application/xhtml+xml" || strings.HasPrefix(mt, "text/html")
}

// firstOf returns the first non-blank value, trimmed.
func firstOf(values ...string) string {
	for _, v := range values {
		if s := collapseSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// nonEmpty returns the non-blank values, trimmed and in order.
func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if s := collapseSpace(v); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// collapseSpace trims a string and folds its internal whitespace runs into
// single spaces, which is what the whitespace-bearing text nodes of an OPF or
// NCX need before they are shown as a title.
func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// humanizeHref turns an archive path into a readable chapter label.
func humanizeHref(href string) string {
	name := path.Base(href)
	if ext := path.Ext(name); ext != "" {
		name = strings.TrimSuffix(name, ext)
	}
	name = strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(name)
	if name = collapseSpace(name); name == "" {
		return href
	}
	return name
}
