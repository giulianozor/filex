package epub

import (
	"archive/zip"
	"bytes"
	"errors"
	"path"
	"strings"
	"testing"
)

// buildEpub assembles an in-memory EPUB and returns its bytes.
func buildEpub(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// The mimetype entry has to come first and be stored uncompressed, per the
	// OCF specification, so a conforming book is what the tests parse.
	mt, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatalf("create mimetype: %v", err)
	}
	if _, err := mt.Write([]byte("application/epub+zip")); err != nil {
		t.Fatalf("write mimetype: %v", err)
	}
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// openTestBook builds a two-chapter EPUB 2 book and parses it.
func openTestBook(t *testing.T) *Book {
	t.Helper()
	data := buildEpub(t, map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`,
		"OEBPS/content.opf": `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:title>The Test Book</dc:title>
    <dc:creator>A. Writer</dc:creator>
    <dc:creator>B. Editor</dc:creator>
    <dc:language>en</dc:language>
    <dc:publisher>filex press</dc:publisher>
    <meta name="cover" content="cover-img"/>
  </metadata>
  <manifest>
    <item id="ch1" href="text/ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="text/ch2.xhtml" media-type="application/xhtml+xml"/>
    <item id="cover-img" href="images/cover.jpg" media-type="image/jpeg"/>
    <item id="css" href="style.css" media-type="text/css"/>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
  </manifest>
  <spine toc="ncx">
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
</package>`,
		"OEBPS/toc.ncx": `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <navMap>
    <navPoint id="n1" playOrder="1"><navLabel><text>Beginning</text></navLabel>
      <content src="text/ch1.xhtml"/>
      <navPoint id="n1a" playOrder="2"><navLabel><text>Nested bit</text></navLabel>
        <content src="text/ch1.xhtml#part2"/>
      </navPoint>
    </navPoint>
    <navPoint id="n2" playOrder="3"><navLabel><text>  The
      End  </text></navLabel><content src="text/ch2.xhtml"/></navPoint>
  </navMap>
</ncx>`,
		"OEBPS/text/ch1.xhtml":   "",
		"OEBPS/text/ch2.xhtml":   "",
		"OEBPS/style.css":        "",
		"OEBPS/images/cover.jpg": "",
	})
	book, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return book
}

func TestOpenReadsMetadataSpineAndTOC(t *testing.T) {
	book := openTestBook(t)

	if book.Title != "The Test Book" {
		t.Errorf("Title = %q, want %q", book.Title, "The Test Book")
	}
	if book.Author != "A. Writer, B. Editor" {
		t.Errorf("Author = %q, want both creators joined", book.Author)
	}
	if book.Language != "en" || book.Publisher != "filex press" {
		t.Errorf("metadata = %q / %q, want en / filex press", book.Language, book.Publisher)
	}
	if book.Cover != "OEBPS/images/cover.jpg" {
		t.Errorf("Cover = %q, want the EPUB 2 meta cover", book.Cover)
	}
	want := []string{"OEBPS/text/ch1.xhtml", "OEBPS/text/ch2.xhtml"}
	if len(book.Docs) != len(want) {
		t.Fatalf("Docs = %d entries (%v), want %d", len(book.Docs), book.Docs, len(want))
	}
	for i, href := range want {
		if book.Docs[i].Href != href {
			t.Errorf("Docs[%d].Href = %q, want %q", i, book.Docs[i].Href, href)
		}
		if book.Docs[i].MediaType != "application/xhtml+xml" {
			t.Errorf("Docs[%d].MediaType = %q", i, book.Docs[i].MediaType)
		}
	}
	if got := book.Docs[0].Title; got != "ch1" {
		t.Errorf("Docs[0].Title = %q, want the file name as a fallback label", got)
	}

	if len(book.TOC) != 2 {
		t.Fatalf("TOC = %d top-level entries, want 2", len(book.TOC))
	}
	if book.TOC[0].Title != "Beginning" || book.TOC[0].Href != "OEBPS/text/ch1.xhtml" {
		t.Errorf("TOC[0] = %+v", book.TOC[0])
	}
	if len(book.TOC[0].Children) != 1 {
		t.Fatalf("TOC[0] has %d children, want the nested navPoint", len(book.TOC[0].Children))
	}
	child := book.TOC[0].Children[0]
	if child.Title != "Nested bit" || child.Href != "OEBPS/text/ch1.xhtml" {
		t.Errorf("nested TOC entry = %+v, want its fragment stripped from the path", child)
	}
	if book.TOC[1].Title != "The End" {
		t.Errorf("TOC[1].Title = %q, want the whitespace collapsed", book.TOC[1].Title)
	}
}

func TestOpenErrors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{
			name: "not a zip",
			data: []byte("this is not a book"),
			want: ErrNotArchive,
		},
		{
			name: "no container",
			data: buildEpub(t, map[string]string{"OEBPS/content.opf": "<package/>"}),
			want: ErrNoPackage,
		},
		{
			name: "container points at a missing package",
			data: buildEpub(t, map[string]string{
				"META-INF/container.xml": `<container><rootfiles><rootfile full-path="gone.opf"/></rootfiles></container>`,
			}),
			want: ErrNoPackage,
		},
		{
			name: "package without a spine",
			data: buildEpub(t, map[string]string{
				"META-INF/container.xml": `<container><rootfiles><rootfile full-path="p.opf"/></rootfiles></container>`,
				"p.opf":                  `<package><manifest><item id="a" href="a.xhtml"/></manifest><spine/></package>`,
				"a.xhtml":                "<html/>",
			}),
			want: ErrNoSpine,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Open(bytes.NewReader(tc.data), int64(len(tc.data)))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Open error = %v, want %v", err, tc.want)
			}
		})
	}
}

// testRewriter serves every entry from the book under a recognisable URL, the
// way the HTTP layer's URL builder does.
func testRewriter(book *Book) URLFunc {
	return func(href string) string { return "/res/" + path.Base(href) + "?p=" + href }
}

const testReaderCSS = "html{color:red}"

func TestDocumentRewritesReferencesAndStripsActiveMarkup(t *testing.T) {
	data := buildEpub(t, map[string]string{
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`,
		"OEBPS/content.opf": `<package version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Doc</dc:title></metadata>
  <manifest>
    <item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="css" href="css/main.css" media-type="text/css"/>
  </manifest>
  <spine><itemref idref="c1"/></spine>
</package>`,
		"OEBPS/css/main.css": `@import "other.css";
body { background: url(../img/bg.png); }`,
		"OEBPS/css/other.css": "",
		"OEBPS/img/bg.png":    "",
		"OEBPS/ch2.xhtml":     "<html><body>Two</body></html>",
		"OEBPS/ch1.xhtml": `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <title>Chapter one</title>
  <base href="http://evil.example/"/>
  <meta http-equiv="refresh" content="0;url=http://evil.example/"/>
  <link rel="stylesheet" href="css/main.css"/>
  <link rel="alternate" href="other.html"/>
  <style>p { background-image: url(img/bg.png) }</style>
  <script>alert('nope')</script>
</head>
<body onload="alert('nope')">
  <p style="color: url(nope.txt)">Hello <em>world</em></p>
  <img src="img/bg.png" alt="a picture"/>
  <img src="http://tracker.example/pixel.gif"/>
  <img srcset="img/bg.png 1x, http://tracker.example/2x.png 2x"/>
  <a href="ch2.xhtml#part2">next</a>
  <a href="http://example.com/">away</a>
  <a href="#here">here</a>
  <p>tail &amp; text</p>
</body>
</html>`,
	})
	book, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	out, err := book.Document("OEBPS/ch1.xhtml", testRewriter(book), testReaderCSS)
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	got := string(out)

	for _, unwanted := range []string{
		"alert(",          // script element
		"evil.example",    // base target and meta refresh
		"tracker.example", // remote image and remote srcset candidate
		`onload=`,         // event handler attribute
		`<base`,           // base element
		`rel="alternate"`, // non-stylesheet link
		`href="other.html"`,
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("output still contains %q:\n%s", unwanted, got)
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(got), "<!DOCTYPE html>") {
		t.Errorf("output does not start with a normalised doctype:\n%s", got)
	}
	if strings.Contains(got, "<?xml") {
		t.Errorf("output still contains the XML declaration:\n%s", got)
	}
	if !strings.Contains(got, testReaderCSS) {
		t.Error("the reader stylesheet was not injected into <head>")
	}
	for _, want := range []string{
		`src="/res/bg.png?p=OEBPS/img/bg.png"`,                // image relative to the document
		`@import url("/res/other.css?p=OEBPS/css/other.css")`, // inlined stylesheet, references resolved
		`body { background: url("/res/bg.png?p=OEBPS/img/bg.png"); }`,
		`background-image: url("/res/bg.png?p=OEBPS/img/bg.png")`, // <style> block rewritten too
		`href="/res/ch2.xhtml?p=OEBPS/ch2.xhtml#part2"`,           // in-book link keeps its fragment
		`<a>away</a>`,                                // link out of the book is neutered
		`<a href="#here">here</a>`,                   // same-document link is untouched
		`srcset="/res/bg.png?p=OEBPS/img/bg.png 1x"`, // the remote candidate is dropped
		`style="color: none"`,                        // a style attribute is rewritten, and a dangling url() is disabled
		`tail &amp; text`,                            // untouched entities stay single-escaped
		`Hello <em>world</em>`,                       // ordinary markup survives
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// The linked stylesheet is inlined, so the document needs no stylesheet
	// request of its own and cannot use one to smuggle a URL past the policy.
	if strings.Contains(got, "<link") {
		t.Errorf("a link element survived rewriting:\n%s", got)
	}
	if !strings.Contains(got, `alt="a picture"`) {
		t.Errorf("an untouched attribute lost its quoting:\n%s", got)
	}
}

func TestDocumentDropsReferencesOutsideTheBook(t *testing.T) {
	data := buildEpub(t, map[string]string{
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="c.opf"/></rootfiles></container>`,
		"c.opf":                  `<package><metadata/><manifest><item id="a" href="a.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="a"/></spine></package>`,
		"a.xhtml": `<html><body>
  <img src="missing.png"/>
  <img src="../../escape.png"/>
  <p>text</p>
</body></html>`,
	})
	book, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	out, err := book.Document("a.xhtml", testRewriter(book), "")
	if err != nil {
		t.Fatalf("Document: %v", err)
	}
	if strings.Contains(string(out), "src=") {
		t.Errorf("dangling and escaping references survived:\n%s", out)
	}
	if !strings.Contains(string(out), "<p>text</p>") {
		t.Errorf("the document body was lost:\n%s", out)
	}
}

func TestResource(t *testing.T) {
	book := openTestBook(t)

	data, mediaType, err := book.Resource("OEBPS/images/cover.jpg")
	if err != nil {
		t.Fatalf("Resource: %v", err)
	}
	if mediaType != "image/jpeg" {
		t.Errorf("mediaType = %q, want image/jpeg from the manifest", mediaType)
	}
	if len(data) != 0 {
		t.Errorf("data = %q, want the (empty) fixture", data)
	}

	// A content document may only be served rewritten, so that the response
	// can be pinned with a content policy.
	if _, _, err := book.Resource("OEBPS/text/ch1.xhtml"); err == nil {
		t.Error("Resource served a content document, want it refused")
	}
	if _, _, err := book.Resource("OEBPS/nothing.xhtml"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Resource(missing) error = %v, want ErrNotFound", err)
	}
}

func TestIsDocument(t *testing.T) {
	book := openTestBook(t)
	if !book.IsDocument("OEBPS/text/ch1.xhtml") {
		t.Error("IsDocument = false for a spine document")
	}
	if book.IsDocument("OEBPS/images/cover.jpg") {
		t.Error("IsDocument = true for an image")
	}
}

func TestResolveRef(t *testing.T) {
	tests := []struct {
		base, ref, want string
	}{
		{"OEBPS/text", "img/a.png", "OEBPS/text/img/a.png"},
		{"OEBPS/text", "../style.css", "OEBPS/style.css"},
		{"OEBPS/text", "./ch1.xhtml", "OEBPS/text/ch1.xhtml"},
		{"OEBPS/text", "a%20b.png", "OEBPS/text/a b.png"},
		{"", "a.xhtml", "a.xhtml"},
		{"OEBPS/text", "ch1.xhtml#frag", "OEBPS/text/ch1.xhtml"},
		{"OEBPS/text", "ch1.xhtml?v=2", "OEBPS/text/ch1.xhtml"},
		{"OEBPS/text", "", ""},
		{"OEBPS/text", "#frag", ""},
		{"OEBPS/text", "https://example.com/a.png", ""},
		{"OEBPS/text", "//example.com/a.png", ""},
		{"OEBPS/text", "data:image/png;base64,AAAA", ""},
		{"OEBPS/text", "mailto:someone@example.com", ""},
		{"OEBPS/text", "chapter1", "OEBPS/text/chapter1"}, // no colon: an ordinary name
		{"OEBPS/text", "../../etc/passwd", "etc/passwd"},  // climbing back to the root is fine
		{"OEBPS/text", "../../../etc/passwd", ""},         // climbing past it is not
		{"", "../etc/passwd", ""},
		{"OEBPS/text", "a\x00b.png", ""},
	}
	for _, tc := range tests {
		if got := resolveRef(tc.base, tc.ref); got != tc.want {
			t.Errorf("resolveRef(%q, %q) = %q, want %q", tc.base, tc.ref, got, tc.want)
		}
	}
}

func TestCollectNavAnchors(t *testing.T) {
	src := `<html><body>
  <nav epub:type="landmarks"><ol><li><a href="cover.xhtml">Cover</a></li></ol></nav>
  <nav epub:type="toc"><ol>
    <li><a href="ch1.xhtml"><span>First <em>chapter</em></span></a>
      <ol><li><a href="ch1.xhtml#s2">Section</a></li></ol>
    </li>
    <li><a href="ch2.xhtml">Second</a></li>
  </ol></nav>
</body></html>`
	got := collectNavAnchors(src)
	want := []anchor{
		{Title: "First chapter", Href: "ch1.xhtml"},
		{Title: "Section", Href: "ch1.xhtml#s2"},
		{Title: "Second", Href: "ch2.xhtml"},
	}
	if len(got) != len(want) {
		t.Fatalf("collectNavAnchors = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("anchor %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	// A navigation document without a <nav> wrapper is all chapters.
	plain := `<html><body><ol><li><a href="a.xhtml">A</a></li></ol></body></html>`
	if got := collectNavAnchors(plain); len(got) != 1 || got[0].Href != "a.xhtml" {
		t.Errorf("collectNavAnchors(plain) = %+v", got)
	}
}

func TestNavDocumentTOCIsUsedWhenThereIsNoNCX(t *testing.T) {
	data := buildEpub(t, map[string]string{
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="c.opf"/></rootfiles></container>`,
		"c.opf": `<package version="3.0">
  <metadata/>
  <manifest>
    <item id="a" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  </manifest>
  <spine><itemref idref="a"/></spine>
</package>`,
		"nav.xhtml": `<html><body><nav epub:type="toc"><ol>
  <li><a href="ch1.xhtml">Only chapter</a></li>
</ol></nav></body></html>`,
		"ch1.xhtml": "<html/>",
	})
	book, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(book.TOC) != 1 || book.TOC[0].Title != "Only chapter" || book.TOC[0].Href != "ch1.xhtml" {
		t.Fatalf("TOC = %+v, want the nav document entry", book.TOC)
	}
}

func TestSpineTOCFallback(t *testing.T) {
	// A book with no navigation file still gets a chapter list built from its
	// spine, so the reader always has somewhere to navigate.
	data := buildEpub(t, map[string]string{
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="c.opf"/></rootfiles></container>`,
		"c.opf": `<package><metadata/><manifest>
  <item id="a" href="the-first-chapter.xhtml" media-type="application/xhtml+xml"/>
</manifest><spine><itemref idref="a"/></spine></package>`,
		"the-first-chapter.xhtml": "<html/>",
	})
	book, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(book.TOC) != 1 || book.TOC[0].Title != "the first chapter" {
		t.Fatalf("TOC = %+v, want a label derived from the file name", book.TOC)
	}
}

func TestLookupIsCaseInsensitive(t *testing.T) {
	data := buildEpub(t, map[string]string{
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="content.opf"/></rootfiles></container>`,
		"content.opf": `<package><metadata/><manifest>
  <item id="a" href="Chapter1.XHTML" media-type="application/xhtml+xml"/>
</manifest><spine><itemref idref="a"/></spine></package>`,
		"Chapter1.XHTML": "<html><body>hi</body></html>",
	})
	book, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// The spine stores the name as the manifest spelled it; a document that
	// links to it with different case has to resolve to the same entry.
	if _, err := book.Document("chapter1.xhtml", testRewriter(book), ""); err != nil {
		t.Errorf("Document with a differently-cased name: %v", err)
	}
}

func TestRewriteContentUnterminatedScript(t *testing.T) {
	// Malformed markup must not break the pass: an unterminated raw-text
	// element ends at the end of the document and still drops its contents.
	got := rewriteContent(`<html><body><p>before</p><script>drop()`, "x", func(reference) string { return "" }, nil)
	if strings.Contains(got, "drop()") {
		t.Errorf("script contents survived: %s", got)
	}
	if !strings.Contains(got, "<p>before</p>") {
		t.Errorf("preceding markup was lost: %s", got)
	}
}

func TestRewriteContentStrayAngleBrackets(t *testing.T) {
	got := rewriteContent(`<p>5 < 6 and 7 > 3</p>`, "", func(reference) string { return "/x" }, nil)
	if !strings.Contains(got, "5 < 6 and 7 > 3") {
		t.Errorf("stray angle brackets were not treated as text: %s", got)
	}
}
