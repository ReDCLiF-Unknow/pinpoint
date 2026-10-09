package web

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pinpoint/internal/store"
)

// item is an annotation as the review shows it, numbered and described.
type item struct {
	store.Annotation
	Number int
	// Page is which page it is on, said simply: "Home page", "/pricing".
	Page string
	// Device is the kind of screen it was made on, and its width.
	Device string
	// Role is what sort of element it is on: "button", "heading".
	Role string
	// Shot is the screenshot's address, if there is one.
	Shot string
}

func items(rv store.Review, list []store.Annotation, share string) []item {
	out := make([]item, len(list))
	for i, a := range list {
		it := item{Annotation: a, Number: i + 1, Page: pageName(a.PageURL), Device: device(a.Viewport), Role: role(a.ElTag)}
		if a.HasShot {
			it.Shot = share + "/" + strconv.Itoa(a.Seq) + ".png"
		}
		out[i] = it
	}
	return out
}

// pageName says which page an address is, as a person would.
func pageName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if (u.Path == "" || u.Path == "/") && u.RawQuery == "" {
		return "Home page"
	}
	p := u.EscapedPath()
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p
}

// device names the kind of screen a width is.
func device(width int) string {
	kind := "desktop"
	switch {
	case width < 600:
		kind = "mobile"
	case width < 1024:
		kind = "tablet"
	}
	return fmt.Sprintf("%s (%dpx)", kind, width)
}

// role says what sort of thing an element is, in words.
func role(tag string) string {
	switch tag {
	case "a":
		return "link"
	case "button":
		return "button"
	case "img", "picture", "svg":
		return "image"
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return "heading"
	case "p":
		return "paragraph"
	case "input", "textarea", "select":
		return "form field"
	case "ul", "ol":
		return "list"
	case "li":
		return "list item"
	case "nav":
		return "navigation"
	case "header":
		return "header"
	case "footer":
		return "footer"
	case "section", "article", "main", "aside":
		return "section"
	case "form":
		return "form"
	case "table":
		return "table"
	case "video":
		return "video"
	case "label":
		return "label"
	}
	return tag
}

// kindPhrase says how something was marked on the page.
func kindPhrase(a store.Annotation) string {
	switch a.Kind {
	case "pin":
		return fmt.Sprintf("a pin, %d%% across and %d%% down the element", pct(a.PosX), pct(a.PosY))
	case "highlight":
		if a.Quote != "" {
			return "a highlight over the text quoted above"
		}
		return "a highlighted area"
	case "box":
		return "a box drawn around it"
	case "draw":
		return "a freehand drawing"
	}
	return a.Kind
}

func pct(f float64) int { return int(math.Round(f * 100)) }

// code formats s as inline Markdown code, whatever backticks it holds.
func code(s string) string {
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

// quoted puts text in double quotes on one line, or as a quote block when it
// runs to several.
func quoted(label, text string) string {
	text = strings.TrimSpace(text)
	if !strings.Contains(text, "\n") {
		return "- " + label + ": \"" + text + "\"\n"
	}
	var b strings.Builder
	b.WriteString("- " + label + ":\n")
	for _, line := range strings.Split(text, "\n") {
		b.WriteString("  > " + strings.TrimRight(line, "\r") + "\n")
	}
	return b.String()
}

// reviewMarkdown is the review as a coding AI reads it: every annotation a
// numbered task, with where it is and what it says.
func reviewMarkdown(rv store.Review, list []store.Annotation, share string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Review of %s (%s)\n\n", rv.SiteHost, plural(len(list), "annotation"))
	who := "A customer"
	if rv.Reviewer != "" {
		who = rv.Reviewer
	}
	when := "not sent yet"
	if rv.Applied() {
		when = "sent " + rv.AppliedAt.Local().Format("2 January 2006")
	}
	fmt.Fprintf(&b, "Feedback from %s, %s. Review: %s\n\n", who, when, share)
	b.WriteString("They marked these up on the live website. Each numbered item is one task. **Change** and **Bug** items " +
		"ask for a change to the site. **Question** items need an answer for the customer, not a code change. **Like it** " +
		"items say what to keep, so leave those as they are. The selector, text and HTML of each element were captured " +
		"from the live page at the screen width given, and each screenshot shows the mark on the page.\n")
	for _, it := range items(rv, list, share) {
		fmt.Fprintf(&b, "\n## %d. %s: %s, %s\n", it.Number, labels[it.Label].Title, it.Page, it.Device)
		fmt.Fprintf(&b, "- Page: %s\n", it.PageURL)
		if it.Selector != "" {
			desc := it.Role
			if it.ElText != "" {
				desc += ", text " + strconv.Quote(it.ElText)
			}
			fmt.Fprintf(&b, "- Element: %s (%s)\n", code(it.Selector), desc)
		}
		if it.Quote != "" {
			b.WriteString(quoted("Highlighted text", it.Quote))
		}
		if strings.TrimSpace(it.Comment) != "" {
			b.WriteString(quoted("Comment", it.Comment))
		} else {
			b.WriteString("- Comment: none; the mark itself is the feedback, so see the screenshot\n")
		}
		fmt.Fprintf(&b, "- Marked with: %s\n", kindPhrase(it.Annotation))
		if it.Shot != "" {
			fmt.Fprintf(&b, "- Screenshot: %s\n", it.Shot)
		}
		if it.ElHTML != "" {
			fence := "```"
			for strings.Contains(it.ElHTML, fence) {
				fence += "`"
			}
			fmt.Fprintf(&b, "- HTML:\n\n  %shtml\n  %s\n  %s\n", fence, strings.ReplaceAll(it.ElHTML, "\n", "\n  "), fence)
		}
	}
	return b.String()
}

// reviewJSON is the review as data.
type reviewJSON struct {
	Site        string           `json:"site"`
	Start       string           `json:"start"`
	URL         string           `json:"url"`
	Reviewer    string           `json:"reviewer,omitempty"`
	Sent        *time.Time       `json:"sent,omitempty"`
	Annotations []annotationJSON `json:"annotations"`
}

type annotationJSON struct {
	Number     int     `json:"number"`
	Label      string  `json:"label"`
	Kind       string  `json:"kind"`
	Comment    string  `json:"comment"`
	Quote      string  `json:"quote,omitempty"`
	Page       string  `json:"page"`
	PageTitle  string  `json:"pageTitle,omitempty"`
	Width      int     `json:"width"`
	Device     string  `json:"device"`
	Selector   string  `json:"selector"`
	Tag        string  `json:"tag"`
	Text       string  `json:"text,omitempty"`
	HTML       string  `json:"html,omitempty"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Screenshot string  `json:"screenshot,omitempty"`
}

func reviewData(rv store.Review, list []store.Annotation, share string) reviewJSON {
	out := reviewJSON{Site: rv.SiteHost, Start: rv.SiteURL, URL: share, Reviewer: rv.Reviewer, Annotations: []annotationJSON{}}
	if rv.Applied() {
		t := rv.AppliedAt.UTC()
		out.Sent = &t
	}
	for _, it := range items(rv, list, share) {
		out.Annotations = append(out.Annotations, annotationJSON{
			Number: it.Number, Label: it.Label, Kind: it.Kind, Comment: it.Comment, Quote: it.Quote,
			Page: it.PageURL, PageTitle: it.PageTitle, Width: it.Viewport, Device: strings.Fields(it.Device)[0],
			Selector: it.Selector, Tag: it.ElTag, Text: it.ElText, HTML: it.ElHTML, X: it.PosX, Y: it.PosY,
			Screenshot: it.Shot,
		})
	}
	return out
}

// format picks what to send for the review URL. Browsers ask for HTML first
// and get the page. A coding AI's fetch tool, curl and the like ask for
// Markdown, or for anything at all, and get the review as text, so pasting
// the plain URL works.
func format(accept string) string {
	best, bestQ := "", -1.0
	// Ties go to the earlier one in this list, which is why Markdown is first.
	for _, f := range []struct{ name, mime string }{
		{"md", "text/markdown"}, {"md", "text/plain"}, {"json", "application/json"}, {"html", "text/html"},
	} {
		if q := quality(accept, f.mime); q > bestQ {
			best, bestQ = f.name, q
		}
	}
	if bestQ <= 0 {
		return "md"
	}
	return best
}

// quality is how much an Accept header wants a type, from 0 to 1. A type
// named outright beats a wildcard that covers it.
func quality(accept, want string) float64 {
	if strings.TrimSpace(accept) == "" {
		accept = "*/*"
	}
	q, specificity := 0.0, -1
	for _, part := range strings.Split(accept, ",") {
		mt, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		s := -1
		switch {
		case mt == want:
			s = 2
		case strings.HasSuffix(mt, "/*") && strings.HasPrefix(want, strings.TrimSuffix(mt, "*")):
			s = 1
		case mt == "*/*":
			s = 0
		}
		if s < specificity || s < 0 {
			continue
		}
		v := 1.0
		if qs, ok := params["q"]; ok {
			if f, err := strconv.ParseFloat(qs, 64); err == nil {
				v = f
			}
		}
		q, specificity = v, s
	}
	return q
}

// reviewView is the shared review page.
type reviewView struct {
	Review   store.Review
	Items    []item
	Pages    int
	Share    string
	Markdown string
	Config   editorConfig
	Version  string
}

func (s *Server) reviewPage(w http.ResponseWriter, r *http.Request) {
	code, want := r.PathValue("code"), ""
	for _, ext := range []string{"md", "json"} {
		if c, ok := strings.CutSuffix(code, "."+ext); ok {
			code, want = c, ext
		}
	}
	rv, err := s.store.ReviewByCode(code)
	if err != nil {
		s.message(w, http.StatusNotFound, "This review isn't here",
			"The link may be incomplete. Ask whoever sent it for the whole link.")
		return
	}
	list, err := s.store.Annotations(rv.ID)
	if err != nil {
		log.Print(err)
		s.message(w, http.StatusInternalServerError, "Something went wrong", "Try again in a moment.")
		return
	}
	share := baseURL(r) + "/r/" + rv.Code
	if want == "" {
		want = format(r.Header.Get("Accept"))
		w.Header().Set("Vary", "Accept")
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Robots-Tag", "noindex")
	switch want {
	case "md":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write([]byte(reviewMarkdown(rv, list, share)))
		return
	case "json":
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		enc.Encode(reviewData(rv, list, share))
		return
	}
	pages := map[string]bool{}
	for _, a := range list {
		pages[a.PageURL] = true
	}
	s.render(w, http.StatusOK, "review.html", reviewView{
		Review:   rv,
		Items:    items(rv, list, share),
		Pages:    len(pages),
		Share:    share,
		Markdown: reviewMarkdown(rv, list, share),
		Version:  s.assetVersion,
		Config: editorConfig{
			Mode:        "view",
			Share:       share,
			Applied:     rv.Applied(),
			Reviewer:    rv.Reviewer,
			Title:       rv.Title,
			Annotations: list,
			reviewFrame: newReviewFrame(r, rv),
		},
	})
}

// shot serves an annotation's screenshot: /r/<code>/<number>.png.
func (s *Server) shot(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutSuffix(r.PathValue("shot"), ".png")
	seq, err := strconv.Atoi(name)
	if !ok || err != nil {
		http.NotFound(w, r)
		return
	}
	rv, err := s.store.ReviewByCode(r.PathValue("code"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	png, err := s.store.Shot(rv.ID, seq)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(png)
}
