package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"pinpoint/internal/store"
)

// unquote reads a string literal the regexps below found.
func unquote(t *testing.T, lit string) string {
	t.Helper()
	if strings.HasPrefix(lit, "'") {
		// A JavaScript literal: the only escape used is \'.
		return strings.ReplaceAll(lit[1:len(lit)-1], `\'`, `'`)
	}
	s, err := strconv.Unquote(lit)
	if err != nil {
		t.Fatalf("can't read %s: %v", lit, err)
	}
	return s
}

// TestGermanCoversEverything finds every piece of text PinPoint translates
// and checks German has it, so a new string can't ship in English only.
func TestGermanCoversEverything(t *testing.T) {
	var want []string
	scan := func(glob string, re *regexp.Regexp) {
		files, _ := filepath.Glob(glob)
		if len(files) == 0 {
			t.Fatalf("no files at %s", glob)
		}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				want = append(want, unquote(t, m[1]))
			}
		}
	}
	scan("templates/*.html", regexp.MustCompile(`(?:\{\{|\()\s*(?:t|th|tf)\s+("(?:[^"\\]|\\.)*")`))
	scan("static/*.js", regexp.MustCompile(`\btr\(('(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*")`))
	scan("*.go", regexp.MustCompile(`\.(?:T|Tf)\(("(?:[^"\\]|\\.)*")`))
	// Text that is translated where it is shown rather than where it is written.
	scan("web.go", regexp.MustCompile(`errors\.New\(("(?:[^"\\]|\\.)*")`))
	scan("web.go", regexp.MustCompile(`fail\(("(?:[^"\\]|\\.)*")`))
	scan("*.go", regexp.MustCompile(`s\.message\(w, r, [^,]+, ("(?:[^"\\]|\\.)*"),\s*("(?:[^"\\]|\\.)*")`))
	scan("*.go", regexp.MustCompile(`s\.message\(w, r, [^,]+, "[^"]*",\s*("(?:[^"\\]|\\.)*")`))
	scan("*.go", regexp.MustCompile(`jsonError\(w, r, [^,]+, ("(?:[^"\\]|\\.)*")`))
	scan("static/app.js", regexp.MustCompile(`\{ title: ('[^']*')`))
	scan("static/app.js", regexp.MustCompile(`(?m)^\s+(?:pin|highlight|box|draw): ('[^']*')`))
	scan("../proxy/proxy.go", regexp.MustCompile(`(?m)^\t\treturn ("[^"]*")$`))
	for _, l := range labels {
		want = append(want, l.Title)
	}
	for _, k := range kinds {
		want = append(want, k.Title)
	}
	for _, tag := range []string{"a", "button", "img", "h1", "p", "input", "ul", "li", "nav", "header", "footer",
		"section", "form", "table", "video", "label"} {
		want = append(want, role(tag))
	}
	for _, w := range []int{320, 800, 1280} {
		kind, _, _ := strings.Cut(device(w), " ")
		want = append(want, kind)
	}
	want = append(want, pageName("https://example.com/"))

	if len(want) < 100 {
		t.Fatalf("found only %d strings; is the scan still finding them?", len(want))
	}
	for _, s := range want {
		if _, ok := german[s]; !ok {
			t.Errorf("no German for %q", s)
		}
	}
}

func TestGermanKeepsPlaceholders(t *testing.T) {
	verbs := regexp.MustCompile(`%[sd]|\{\w+\}|<[^>]+>`)
	for en, de := range german {
		if a, b := verbs.FindAllString(en, -1), verbs.FindAllString(de, -1); strings.Join(a, " ") != strings.Join(b, " ") {
			t.Errorf("%q has %v, its German %v", en, a, b)
		}
	}
}

func TestLangOf(t *testing.T) {
	for _, c := range []struct {
		accept, cookie string
		want           lang
	}{
		{"", "", en},
		{"de-DE,de;q=0.9,en;q=0.8", "", de},
		{"en-US,en;q=0.9,de;q=0.8", "", en},
		{"fr-FR,de;q=0.7", "", de},
		{"fr-FR", "", en},
		{"de-AT", "en", en},
		{"en-US", "de", de},
		{"en-US", "xx", en},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Language", c.accept)
		if c.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "lang", Value: c.cookie})
		}
		if got := langOf(r); got != c.want {
			t.Errorf("Accept-Language %q, cookie %q: got %s, want %s", c.accept, c.cookie, got, c.want)
		}
	}
}

func TestPagesInGerman(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rv, key, err := st.CreateReview("https://example.com/", "example.com", "Example")
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.AddAnnotation(rv.ID, store.Annotation{Kind: "pin", Label: "bug", Comment: "Kaputt", PageURL: "https://example.com/",
		Viewport: 1280, Selector: "a.btn", ElTag: "a", ElText: "Mehr", PosX: .5, PosY: .5, Geometry: []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(st)
	get := func(path, accept string) string {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Accept", accept)
		r.Header.Set("Accept-Language", "de-DE,de;q=0.9")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w.Body.String()
	}

	for path, want := range map[string][]string{
		"/":                    {`<html lang="de">`, "Feedback starten", "<strong>Markieren</strong>", `"{n} Anmerkungen"`},
		"/edit/" + key:         {`<html lang="de">`, "Feedback zu example.com", "Absenden", "Hervorheben", `"Save":"Speichern"`},
		"/r/" + rv.Code:        {`<html lang="de">`, "Feedback zu example.com", "Startseite", "Fehler", "Desktop (1280px)", "Link, Text „Mehr“"},
		"/r/nothing-like-this": {"Dieses Feedback gibt es hier nicht"},
	} {
		body := get(path, "text/html")
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: no %q in the German page", path, w)
			}
		}
	}

	// What the coding AI reads stays English.
	if md := get("/r/"+rv.Code, "text/markdown"); !strings.Contains(md, "## 1. Bug: Home page, desktop (1280px)") {
		t.Errorf("the Markdown isn't English:\n%s", md)
	}
}
