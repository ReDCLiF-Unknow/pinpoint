package proxy

import (
	"bytes"
	"html"
	"io"
	"net/url"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

// Decode converts an HTML document to UTF-8, working out its encoding from
// the Content-Type header, a byte order mark or its own <meta charset>.
func Decode(body []byte, contentType string) []byte {
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return body
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return body
	}
	return out
}

// BaseURL is where the page's relative addresses lead: its own <base href>
// if it has one, and otherwise the page itself.
func BaseURL(doc []byte, page *url.URL) *url.URL {
	z := xhtml.NewTokenizer(bytes.NewReader(doc))
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return page
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			if string(name) == "body" {
				return page
			}
			if string(name) != "base" || !hasAttr {
				continue
			}
			for {
				k, v, more := z.TagAttr()
				if string(k) == "href" {
					if b, err := page.Parse(strings.TrimSpace(string(v))); err == nil && (b.Scheme == "http" || b.Scheme == "https") {
						return b
					}
				}
				if !more {
					break
				}
			}
		}
	}
}

// Rewrite prepares a page for the review frame. It writes the page to w with:
//
//   - a <base> pointing at the real site, so everything the page loads comes
//     straight from there;
//   - inject (the review script) before anything else in <head>, so it runs
//     before the page's own scripts;
//   - the page's own Content-Security-Policy and referrer <meta> elements
//     removed, as they would stop the review script or override the frame's
//     referrer policy;
//   - its charset declared as UTF-8, which Decode has made it;
//   - a <meta http-equiv="refresh"> sent through the frame, as the real site
//     would refuse to be shown in it.
//
// Everything else is passed through byte for byte.
func Rewrite(w io.Writer, doc []byte, base *url.URL, inject string, frameURL func(string) string) error {
	head := `<base href="` + html.EscapeString(base.String()) + `">` + inject
	injected := false
	z := xhtml.NewTokenizer(bytes.NewReader(doc))
	for {
		tt := z.Next()
		if tt == xhtml.ErrorToken {
			if !injected {
				io.WriteString(w, head)
			}
			if z.Err() == io.EOF {
				return nil
			}
			return z.Err()
		}
		raw := z.Raw()
		if !injected {
			switch tt {
			case xhtml.DoctypeToken, xhtml.CommentToken:
				w.Write(raw)
				continue
			case xhtml.TextToken:
				if len(bytes.TrimSpace(raw)) == 0 {
					w.Write(raw)
					continue
				}
			case xhtml.StartTagToken:
				name, _ := z.TagName()
				switch string(name) {
				case "html":
					w.Write(raw)
					continue
				case "head":
					w.Write(raw)
					io.WriteString(w, head)
					injected = true
					continue
				}
			}
			// Anything else starts the document's content: with no <head>
			// of its own, what goes before it lands in the implied one.
			io.WriteString(w, head)
			injected = true
		}
		if tt == xhtml.StartTagToken || tt == xhtml.SelfClosingTagToken {
			if out, changed := rewriteTag(z, raw, base, frameURL); changed {
				io.WriteString(w, out)
				continue
			}
		}
		w.Write(raw)
	}
}

var refreshURL = regexp.MustCompile(`(?i)^(\s*\d*\s*[;,]\s*(?:url\s*=\s*)?)['"]?([^'"]*)['"]?\s*$`)

// rewriteTag returns what to write instead of the tag the tokenizer is on,
// and false to write it as it was.
func rewriteTag(z *xhtml.Tokenizer, raw []byte, base *url.URL, frameURL func(string) string) (string, bool) {
	name, hasAttr := z.TagName()
	switch string(name) {
	case "base":
		// Ours comes first and already says where the page's own one
		// pointed. A target on it would only send links out of the frame,
		// and the review script decides where links go anyway.
		return "", true
	case "meta":
	default:
		return "", false
	}
	if !hasAttr {
		return "", false
	}
	attrs := map[string]string{}
	for {
		k, v, more := z.TagAttr()
		attrs[strings.ToLower(string(k))] = string(v)
		if !more {
			break
		}
	}
	equiv := strings.ToLower(attrs["http-equiv"])
	switch {
	case attrs["charset"] != "", equiv == "content-type":
		return `<meta charset="utf-8">`, true
	case equiv == "content-security-policy", equiv == "content-security-policy-report-only",
		strings.EqualFold(attrs["name"], "referrer"):
		return "", true
	case equiv == "refresh":
		m := refreshURL.FindStringSubmatch(attrs["content"])
		if m == nil || strings.TrimSpace(m[2]) == "" {
			return "", false
		}
		target, err := base.Parse(strings.TrimSpace(m[2]))
		if err != nil {
			return "", true
		}
		return `<meta http-equiv="refresh" content="` + html.EscapeString(m[1]+frameURL(target.String())) + `">`, true
	}
	return "", false
}

// Title is the text of the page's <title>, if it has one.
func Title(doc []byte) string {
	z := xhtml.NewTokenizer(bytes.NewReader(doc))
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return ""
		case xhtml.StartTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "title":
				if z.Next() == xhtml.TextToken {
					return strings.Join(strings.Fields(html.UnescapeString(string(z.Text()))), " ")
				}
				return ""
			case "body":
				return ""
			}
		}
	}
}
