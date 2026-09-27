package epub

import (
	"html"
	"regexp"
	"strings"
)

// reference is a URL reference taken from a content document, split into the
// part that addresses a file and the fragment that addresses a position inside
// it.
type reference struct {
	// Raw is the reference as written, fragment included.
	Raw string
	// Path is the file part with the query removed, empty for a bare fragment
	// such as "#chapter-2".
	Path string
	// Fragment is the text after the first '#', without the '#'.
	Fragment string
	// External reports a reference that names its own scheme or host
	// ("https://example.com/x.png"). The reader drops those rather than
	// fetching them, so a book cannot phone home from the reading view.
	External bool
}

// splitReference separates a raw reference into its file and fragment parts.
func splitReference(raw string) reference {
	raw = strings.TrimSpace(raw)
	ref := reference{Raw: raw}
	if i := strings.IndexAny(raw, "#?"); i >= 0 {
		ref.Path = raw[:i]
		if raw[i] == '#' {
			ref.Fragment = raw[i+1:]
		}
	} else {
		ref.Path = raw
	}
	ref.External = ref.Path != "" && (strings.HasPrefix(ref.Path, "//") || hasScheme(ref.Path))
	return ref
}

// ---- markup tokenizer --------------------------------------------------------

// tokenKind distinguishes the regions of a document that the rewriter treats
// differently.
type tokenKind int

const (
	tokText tokenKind = iota
	tokComment
	tokDecl // <!DOCTYPE ...>
	tokPI   // <?xml ... ?> and other processing instructions
	tokStart
	tokEnd
)

// token is one region of a source document.
type token struct {
	Kind  tokenKind
	Start int
	End   int
	Tag   tag
	Text  string
}

// attribute is one parsed attribute of a tag.
type attribute struct {
	Name  string
	Value string
	Quote byte
	// ValueStart and ValueEnd delimit the raw value inside the source, so an
	// untouched attribute can be copied byte-for-byte instead of being
	// re-escaped (which would turn "&amp;" into "&amp;amp;").
	ValueStart int
	ValueEnd   int
}

// tag is a parsed markup tag.
type tag struct {
	// Name is the lower-cased element name, used for comparisons.
	Name string
	// NameEnd is the offset just past the name in the source.
	NameEnd    int
	Attrs      []attribute
	Start      int // offset of '<'
	End        int // offset just past '>'
	Open       int // offset just past '<' and any '/'
	Closing    bool
	SelfClosed bool
}

// attr returns the named attribute and its index, or a zero attribute and -1.
func (t tag) attr(name string) (attribute, int) {
	for i, a := range t.Attrs {
		if a.Name == name {
			return a, i
		}
	}
	return attribute{}, -1
}

// rawTextElements hold character data that an HTML parser never looks for tags
// in, so the tokenizer must hand their contents over as a single text run.
var rawTextElements = map[string]bool{
	"script":   true,
	"style":    true,
	"textarea": true,
	"title":    true,
}

// voidElements never have contents, so a start tag for one must not push an
// element onto the open-element stack.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// droppedElements are removed together with their contents. Each is a scripting
// or navigation vector (script, base, frames, plugins) or markup that only
// exists to be shown when scripts are unavailable.
var droppedElements = map[string]bool{
	"script": true, "base": true, "iframe": true, "frame": true,
	"frameset": true, "object": true, "embed": true, "applet": true,
	"noscript": true,
}

// urlAttributes carry a single URL. The value is dropped when the target is
// not inside the book; a remote target is never fetched from the reading view.
var urlAttributes = map[string]bool{
	"src": true, "href": true, "poster": true, "data": true,
	"xlink:href": true, "longdesc": true, "background": true,
	"cite": true, "action": true, "formaction": true,
}

// tokenize walks src and calls fn for every markup token in document order.
// Scanning stops early when fn returns false. Comments, doctypes and
// processing instructions are reported as their own kinds so the caller can
// drop them, and the contents of a raw-text element are reported as one text
// token with no tags recognised inside, which is how an HTML parser treats
// them.
func tokenize(src string, fn func(token) bool) {
	i := 0
	for i < len(src) {
		lt := strings.IndexByte(src[i:], '<')
		if lt < 0 {
			emitText(src, i, len(src), fn)
			return
		}
		if lt > 0 {
			if !emitText(src, i, i+lt, fn) {
				return
			}
			i += lt
		}
		switch {
		case strings.HasPrefix(src[i:], "<!--"):
			end := i + 4 + indexAfter(src[i+4:], "-->")
			if !fn(token{Kind: tokComment, Start: i, End: end}) {
				return
			}
			i = end

		case strings.HasPrefix(src[i:], "<![CDATA["):
			end := i + 9 + indexAfter(src[i+9:], "]]>")
			if !emitText(src, i, end, fn) {
				return
			}
			i = end

		case strings.HasPrefix(src[i:], "<!"):
			end := i + 1 + indexAfter(src[i+1:], ">")
			if !fn(token{Kind: tokDecl, Start: i, End: end}) {
				return
			}
			i = end

		case strings.HasPrefix(src[i:], "<?"):
			end := i + 2 + indexAfter(src[i+2:], "?>")
			if !fn(token{Kind: tokPI, Start: i, End: end}) {
				return
			}
			i = end

		default:
			t, ok := parseTag(src, i)
			if !ok {
				// A '<' that starts no tag is ordinary character data.
				if !emitText(src, i, i+1, fn) {
					return
				}
				i++
				continue
			}
			kind := tokStart
			if t.Closing {
				kind = tokEnd
			}
			if !fn(token{Kind: kind, Start: i, End: t.End, Tag: t}) {
				return
			}
			i = t.End
			if kind == tokStart && !t.SelfClosed && rawTextElements[t.Name] {
				contentEnd, tagEnd, closed := rawTextEnd(src, i, t.Name)
				if !emitText(src, i, contentEnd, fn) {
					return
				}
				if !closed {
					// Unterminated element: emit the end tag the source lacks
					// so the caller's element stack stays balanced.
					if !fn(token{Kind: tokEnd, Start: contentEnd, End: contentEnd, Tag: tag{Name: t.Name}}) {
						return
					}
					i = contentEnd
					continue
				}
				if tagEnd > contentEnd {
					end, ok := parseTag(src, contentEnd)
					if !ok {
						return
					}
					if !fn(token{Kind: tokEnd, Start: contentEnd, End: end.End, Tag: end}) {
						return
					}
				}
				i = tagEnd
			}
		}
	}
}

// emitText reports a run of character data.
func emitText(src string, start, end int, fn func(token) bool) bool {
	return fn(token{Kind: tokText, Start: start, End: end, Text: src[start:end]})
}

// indexAfter returns the offset just past the first occurrence of sep in s, or
// len(s) when sep is absent.
func indexAfter(s, sep string) int {
	if i := strings.Index(s, sep); i >= 0 {
		return i + len(sep)
	}
	return len(s)
}

// rawTextEnd finds where a raw-text element's contents end and where its
// closing tag ends. closed is false when the element is never closed.
func rawTextEnd(src string, from int, name string) (contentEnd, tagEnd int, closed bool) {
	for j := from; j < len(src); {
		lt := strings.IndexByte(src[j:], '<')
		if lt < 0 {
			break
		}
		j += lt
		if j+1 >= len(src) || src[j+1] != '/' {
			j++
			continue
		}
		t, ok := parseTag(src, j)
		if !ok {
			j++
			continue
		}
		if t.Closing && t.Name == name {
			return j, t.End, true
		}
		j = t.End
	}
	return len(src), len(src), false
}

// parseTag parses the tag starting at src[i], which must be '<'. It reports
// false when the region does not start a well-formed tag.
func parseTag(src string, i int) (tag, bool) {
	t := tag{Start: i}
	i++
	t.Open = i
	if i < len(src) && src[i] == '/' {
		t.Closing = true
		i++
	}
	start := i
	for i < len(src) && isTagNameByte(src[i]) {
		i++
	}
	t.Name = strings.ToLower(src[start:i])
	t.NameEnd = i
	if t.Name == "" {
		return t, false
	}
	for i < len(src) {
		for i < len(src) && isSpaceByte(src[i]) {
			i++
		}
		if i >= len(src) {
			return t, false
		}
		if src[i] == '>' {
			t.End = i + 1
			return t, true
		}
		if src[i] == '/' {
			if i+1 < len(src) && src[i+1] == '>' {
				t.SelfClosed = true
				t.End = i + 2
				return t, true
			}
			i++
			continue
		}
		nameStart := i
		for i < len(src) && !isSpaceByte(src[i]) && src[i] != '=' && src[i] != '>' {
			i++
		}
		if i == nameStart {
			i++ // not the start of an attribute; skip the byte to make progress
			continue
		}
		a := attribute{Name: strings.ToLower(src[nameStart:i])}
		for i < len(src) && isSpaceByte(src[i]) {
			i++
		}
		if i < len(src) && src[i] == '=' {
			i++
			for i < len(src) && isSpaceByte(src[i]) {
				i++
			}
			if i < len(src) && (src[i] == '"' || src[i] == '\'') {
				a.Quote = src[i]
				i++
				a.ValueStart = i
				for i < len(src) && src[i] != a.Quote {
					i++
				}
				a.ValueEnd = i
				a.Value = src[a.ValueStart:a.ValueEnd]
				if i < len(src) {
					i++ // closing quote
				}
			} else {
				a.ValueStart = i
				for i < len(src) && !isSpaceByte(src[i]) && src[i] != '>' {
					i++
				}
				a.ValueEnd = i
				a.Value = src[a.ValueStart:a.ValueEnd]
			}
		}
		t.Attrs = append(t.Attrs, a)
	}
	return t, false
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func isTagNameByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == ':' || c == '-' || c == '_' || c == '.':
		return true
	}
	return false
}

// ---- content rewriting -------------------------------------------------------

// styler inlines a referenced stylesheet, returning its already-rewritten text,
// or "" when it cannot be inlined.
type styler func(ref reference) string

// rewriteContent turns a content document into something a browser can render
// from inside the archive: URL references are replaced by rewrite, linked
// stylesheets are inlined through inline, and script, frames, plugins and
// meta-refresh are removed. injectedCSS is added to the document's own styling.
//
// The Content-Security-Policy on the response is what actually keeps a hostile
// book inert; the removals below keep the markup honest — a book cannot depend
// on a script it is no longer allowed to run — and keep those bytes off the
// wire entirely.
func rewriteContent(src, injectedCSS string, rewrite func(reference) string, inline styler) string {
	var out strings.Builder
	out.Grow(len(src) + len(injectedCSS) + 1024)

	var skip []string // open elements inside a dropped subtree
	rawOpen := ""     // raw-text element currently open
	injected := false
	docType := false

	inject := func() {
		if injected {
			return
		}
		injected = true
		out.WriteString("<style>")
		out.WriteString(injectedCSS)
		out.WriteString("</style>")
	}

	tokenize(src, func(tk token) bool {
		switch tk.Kind {
		case tokComment, tokPI:
			return true

		case tokDecl:
			// Emit one normalised doctype: dropping it would leave the browser
			// in quirks mode and quietly change the book's box model.
			if !docType {
				docType = true
				out.WriteString("<!DOCTYPE html>")
			}
			return true

		case tokText:
			switch {
			case len(skip) > 0:
			case rawOpen == "style":
				out.WriteString(rewriteCSS(tk.Text, rewrite))
			default:
				out.WriteString(tk.Text)
			}
			return true

		case tokStart:
			if len(skip) > 0 {
				if !tk.Tag.SelfClosed && !voidElements[tk.Tag.Name] {
					skip = append(skip, tk.Tag.Name)
				}
				return true
			}
			switch {
			case droppedElements[tk.Tag.Name]:
				if !tk.Tag.SelfClosed && !voidElements[tk.Tag.Name] {
					skip = append(skip, tk.Tag.Name)
				}
				return true
			case tk.Tag.Name == "meta" && isRedirectMeta(tk.Tag):
				// A meta refresh would navigate the frame out from under the
				// reader, to wherever the book likes.
				return true
			case tk.Tag.Name == "link":
				writeStylesheetLink(&out, tk.Tag, inline)
				return true
			}
			if rawTextElements[tk.Tag.Name] {
				rawOpen = tk.Tag.Name
			}
			writeTag(&out, src, tk.Tag, rewrite)
			if tk.Tag.Name == "head" {
				inject()
			}
			return true

		case tokEnd:
			if len(skip) > 0 {
				if n := len(skip) - 1; skip[n] == tk.Tag.Name {
					skip = skip[:n]
				}
				return true
			}
			if rawOpen != "" {
				if rawOpen == tk.Tag.Name {
					rawOpen = ""
				} else {
					// The end tag of a raw-text element nested inside another
					// one: copy it through unchanged.
					out.WriteString(src[tk.Start:tk.End])
					return true
				}
			}
			out.WriteString("</" + tk.Tag.Name + ">")
			return true
		}
		return true
	})

	// A document with no <head> still gets the reader's baseline styling, just
	// after its own content, which a browser applies all the same.
	inject()
	return out.String()
}

// isRedirectMeta reports whether a meta element redirects the document.
func isRedirectMeta(t tag) bool {
	a, _ := t.attr("http-equiv")
	return strings.EqualFold(strings.TrimSpace(a.Value), "refresh")
}

// writeStylesheetLink replaces a <link rel="stylesheet"> with the stylesheet's
// contents inline, so a rendered document needs no stylesheet response of its
// own and cannot be used to smuggle a request past the response policy. Any
// other link element is dropped: alternate and canonical links have no meaning
// for a document served out of an archive.
func writeStylesheetLink(out *strings.Builder, t tag, inline styler) {
	rel, _ := t.attr("rel")
	if !strings.EqualFold(strings.TrimSpace(rel.Value), "stylesheet") {
		return
	}
	href, _ := t.attr("href")
	css := inline(splitReference(href.Value))
	if css == "" {
		return
	}
	out.WriteString("<style>")
	out.WriteString(css)
	out.WriteString("</style>")
}

// writeTag emits a start tag with its URL attributes rewritten, its event
// handler attributes dropped, and every other byte of the tag preserved.
func writeTag(out *strings.Builder, src string, t tag, rewrite func(reference) string) {
	out.WriteString(src[t.Start:t.NameEnd])
	for _, a := range t.Attrs {
		// Decide before writing: a dropped attribute must leave no trace, and
		// a valueless one keeps its valueless form.
		r := attrOut{value: src[a.ValueStart:a.ValueEnd], verbatim: true}
		if a.Quote == 0 && a.ValueStart == a.ValueEnd {
			r.value, r.verbatim = "", false
			r.valueless = true
		}
		switch {
		case strings.HasPrefix(a.Name, "on"):
			// Event handler attribute. The response policy blocks it anyway;
			// there is no reason to ship it.
			r.drop = true
		case r.valueless:
		case a.Name == "style":
			r.value, r.verbatim = rewriteCSS(a.Value, rewrite), false
		case a.Name == "srcset":
			if value, ok := rewriteSrcset(a.Value, rewrite); ok {
				r.value, r.verbatim = value, false
			} else {
				r.drop = true
			}
		case urlAttributes[a.Name]:
			value, ok := rewriteURL(a.Value, rewrite)
			if !ok {
				// Drop the attribute rather than emit an empty value: src=""
				// and href="" both resolve to the current document, which would
				// turn a discarded reference into a self-request.
				r.drop = true
				break
			}
			r.value, r.verbatim = value, false
		}
		if r.drop {
			continue
		}
		out.WriteString(" ")
		out.WriteString(a.Name)
		if r.valueless {
			continue
		}
		out.WriteString("=")
		// A verbatim value cannot contain the quote it was delimited by, so
		// reusing writeAttrValue leaves its escaping a no-op; the point is
		// that the original quoting is reproduced either way.
		writeAttrValue(out, r.value, a.Quote)
	}
	out.WriteString(">")
}

// attrOut is what to do with one attribute of a rewritten tag.
type attrOut struct {
	drop      bool
	valueless bool
	verbatim  bool // copy the source value through as it stands
	value     string
}

// writeAttrValue writes an attribute value, quoting and escaping it when the
// original was unquoted and the replacement needs it, or when the replacement
// contains the attribute's own quote character.
func writeAttrValue(out *strings.Builder, value string, quote byte) {
	if quote == 0 && (value == "" || strings.ContainsAny(value, " \t\n\r\f\v")) {
		// An unquoted value cannot contain whitespace or be empty.
		quote = '"'
	}
	if quote == 0 {
		out.WriteString(value)
		return
	}
	q := string(quote)
	out.WriteString(q)
	if strings.Contains(value, q) {
		out.WriteString(html.EscapeString(value))
	} else {
		out.WriteString(value)
	}
	out.WriteString(q)
}

// rewriteURL maps one attribute value to the URL to emit. It reports false
// when the attribute should be dropped, which is how a remote or dangling
// reference is discarded. An anchor's attribute is dropped the same way: the
// link text stays where it is, but without an href it is not a link, so there
// is nothing left to click and nothing that could navigate anywhere.
func rewriteURL(raw string, rewrite func(reference) string) (string, bool) {
	ref := splitReference(raw)
	if ref.Path == "" {
		if ref.Fragment == "" {
			return "", false
		}
		// A bare "#id" points inside the current document and still works.
		return "#" + ref.Fragment, true
	}
	if ref.External {
		return "", false
	}
	url := rewrite(ref)
	if url == "" {
		return "", false
	}
	if ref.Fragment != "" {
		url += "#" + ref.Fragment
	}
	return url, true
}

// rewriteSrcset maps a srcset attribute to a new value, dropping the candidates
// that are not part of the book. It reports false when nothing is left, so the
// attribute itself is dropped.
func rewriteSrcset(raw string, rewrite func(reference) string) (string, bool) {
	var kept []string
	for _, candidate := range strings.Split(raw, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		fields := strings.Fields(candidate)
		url := rewrite(splitReference(fields[0]))
		if url == "" {
			continue
		}
		if len(fields) > 1 {
			candidate = url + " " + strings.Join(fields[1:], " ")
		} else {
			candidate = url
		}
		kept = append(kept, candidate)
	}
	if len(kept) == 0 {
		return "", false
	}
	return strings.Join(kept, ", "), true
}

// ---- CSS rewriting -----------------------------------------------------------

var (
	// cssURLPattern matches a url() reference in a stylesheet or style
	// attribute, quoted or bare.
	cssURLPattern = regexp.MustCompile(`(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^)'"]*))\s*\)`)
	// cssImportPattern matches a quoted @import target, which most stylesheets
	// write without the url() wrapper.
	cssImportPattern = regexp.MustCompile(`(?i)@import\s+(?:"([^"]*)"|'([^']*)')`)
)

// rewriteCSS points every URL a stylesheet references at the book's resources.
// A reference that is not part of the archive becomes "none", which disables
// the image or font it belonged to instead of leaving behind a request that
// would fail anyway; a stylesheet @import is simply dropped.
func rewriteCSS(css string, rewrite func(reference) string) string {
	resolve := func(raw string) (string, bool) {
		if strings.TrimSpace(raw) == "" {
			return "", false
		}
		ref := splitReference(raw)
		url := rewrite(ref)
		if url == "" {
			return "", false
		}
		if ref.Fragment != "" {
			url += "#" + ref.Fragment
		}
		return cssQuote(url), true
	}
	css = cssURLPattern.ReplaceAllStringFunc(css, func(match string) string {
		groups := cssURLPattern.FindStringSubmatch(match)
		if len(groups) == 0 {
			return match
		}
		if url, ok := resolve(firstSetGroup(groups[1:])); ok {
			return "url(" + url + ")"
		}
		return "none"
	})
	// The @import pattern deliberately stops at the closing quote, so the
	// semicolon the source wrote after it is left in place.
	css = cssImportPattern.ReplaceAllStringFunc(css, func(match string) string {
		groups := cssImportPattern.FindStringSubmatch(match)
		if len(groups) == 0 {
			return match
		}
		if url, ok := resolve(firstSetGroup(groups[1:])); ok {
			return "@import url(" + url + ")"
		}
		return ""
	})
	return css
}

func firstSetGroup(groups []string) string {
	for _, g := range groups {
		if g != "" {
			return g
		}
	}
	return ""
}

// cssQuote renders a URL as the string literal of a url() token, escaping the
// characters that would end it early.
func cssQuote(url string) string {
	url = strings.ReplaceAll(url, `\`, `\\`)
	url = strings.ReplaceAll(url, `"`, `%22`)
	url = strings.NewReplacer("\n", "", "\r", "").Replace(url)
	return `"` + url + `"`
}

// ---- navigation document scanning -------------------------------------------

// anchor is one link found in a navigation document.
type anchor struct {
	Title string
	Href  string
}

// collectNavAnchors returns the links of an EPUB 3 navigation document. Only
// the contents of a <nav> element are considered when the document has one, and
// the nav labelled as the table of contents wins over the others: a navigation
// document also lists a "landmarks" section, which is not a chapter list.
func collectNavAnchors(src string) []anchor {
	type navGroup struct {
		isTOC   bool
		anchors []anchor
	}
	var navs []*navGroup
	var current *navGroup
	var depth int

	var pending *anchor
	var text strings.Builder

	flush := func() {
		if pending == nil {
			return
		}
		pending.Title = collapseSpace(text.String())
		text.Reset()
		if current != nil && pending.Href != "" {
			current.anchors = append(current.anchors, *pending)
		}
		pending = nil
	}

	tokenize(src, func(tk token) bool {
		switch tk.Kind {
		case tokStart:
			switch tk.Tag.Name {
			case "nav":
				depth++
				if depth == 1 {
					// A nested nav belongs to the outermost one.
					current = &navGroup{isTOC: isTOCNav(tk.Tag)}
					navs = append(navs, current)
				}
			case "a":
				if current == nil {
					return true
				}
				flush()
				href, _ := tk.Tag.attr("href")
				pending = &anchor{Href: strings.TrimSpace(href.Value)}
			}
		case tokEnd:
			switch tk.Tag.Name {
			case "a":
				flush()
			case "nav":
				if depth > 0 {
					depth--
					if depth == 0 {
						current = nil
					}
				}
			}
		case tokText:
			if pending != nil {
				text.WriteString(tk.Text)
			}
		}
		return true
	})
	flush()

	switch {
	case len(navs) == 0:
		// A navigation document that does not wrap its contents in a <nav>:
		// every link in it is a chapter entry.
		return collectAllAnchors(src)
	case navs[0].isTOC:
		return navs[0].anchors
	}
	for _, g := range navs {
		if g.isTOC {
			return g.anchors
		}
	}
	return navs[0].anchors
}

// isTOCNav reports whether a nav element is labelled as the table of contents.
func isTOCNav(t tag) bool {
	typ, _ := t.attr("epub:type")
	return strings.EqualFold(strings.TrimSpace(typ.Value), "toc")
}

// collectAllAnchors returns every link in a document, used when a navigation
// document does not wrap its table of contents in a <nav> element.
func collectAllAnchors(src string) []anchor {
	var anchors []anchor
	var pending *anchor
	var text strings.Builder

	flush := func() {
		if pending == nil {
			return
		}
		if pending.Href != "" {
			pending.Title = collapseSpace(text.String())
			anchors = append(anchors, *pending)
		}
		text.Reset()
		pending = nil
	}

	tokenize(src, func(tk token) bool {
		switch tk.Kind {
		case tokStart:
			if tk.Tag.Name == "a" {
				flush()
				href, _ := tk.Tag.attr("href")
				pending = &anchor{Href: strings.TrimSpace(href.Value)}
			}
		case tokEnd:
			if tk.Tag.Name == "a" {
				flush()
			}
		case tokText:
			if pending != nil {
				text.WriteString(tk.Text)
			}
		}
		return true
	})
	flush()
	return anchors
}
