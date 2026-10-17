package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"pinpoint/internal/store"
)

func TestLanguage(t *testing.T) {
	tests := []struct {
		name, cookie, accept, want string
	}{
		{"nothing said", "", "", "en"},
		{"German browser", "", "de-DE,de;q=0.9,en;q=0.8", "de"},
		{"plain de", "", "de", "de"},
		{"English first", "", "en-US,en;q=0.9,de;q=0.8", "en"},
		{"German preferred by weight", "", "en;q=0.5, de;q=0.9", "de"},
		{"only other languages", "", "fr-FR,fr;q=0.9", "en"},
		{"German after French", "", "fr,de;q=0.7", "de"},
		{"refused", "", "de;q=0", "en"},
		{"nonsense weight", "", "de;q=lots", "en"},
		{"capitals and spaces", "", "  DE-at ;q=0.8", "de"},
		{"switch beats browser", "en", "de-DE", "en"},
		{"switch to German", "de", "en-US", "de"},
		{"unknown switch value", "fr", "de", "de"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if tt.accept != "" {
				r.Header.Set("Accept-Language", tt.accept)
			}
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "lang", Value: tt.cookie})
			}
			if got := language(r); got != tt.want {
				t.Errorf("language = %q, want %q", got, tt.want)
			}
		})
	}
}

// Every translation must fill in the same values as the English it replaces,
// or a page would show %!s(MISSING) or a bare {0}.
func TestTranslationsKeepTheirBlanks(t *testing.T) {
	blank := regexp.MustCompile(`%[sd]|\{\d\}|<[^>]+>`)
	for en, de := range germanText {
		if a, b := blank.FindAllString(en, -1), blank.FindAllString(de, -1); strings.Join(a, " ") != strings.Join(b, " ") {
			t.Errorf("%q has %v, its translation %q has %v", en, a, de, b)
		}
		if strings.TrimSpace(de) == "" {
			t.Errorf("%q is translated as nothing", en)
		}
	}
	if got := tr(german, "Not a sentence PinPoint knows"); got != "Not a sentence PinPoint knows" {
		t.Errorf("untranslated text came out as %q", got)
	}
	if got := tr(german, "on %d pages", 3); got != "auf 3 Seiten" {
		t.Errorf("tr with a value = %q", got)
	}
}

func TestPagesInBothLanguages(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rv, key, err := st.CreateReview("https://example.com/", "example.com", "Example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAnnotation(rv.ID, store.Annotation{Kind: "pin", Label: "bug", Comment: "Too small",
		PageURL: "https://example.com/", Viewport: 390, Selector: "a.more", ElTag: "a", ElText: "More",
		PosX: 0.5, PosY: 0.5, Geometry: json.RawMessage(`{"x":1,"y":2}`)}); err != nil {
		t.Fatal(err)
	}
	if err := st.Apply(rv.ID, "Sam"); err != nil {
		t.Fatal(err)
	}
	srv := New(st)

	get := func(path, accept string) (int, string, http.Header) {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Accept-Language", accept)
		r.Header.Set("Accept", "text/html") // as a browser asks
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		b, _ := io.ReadAll(w.Result().Body)
		return w.Code, string(b), w.Header()
	}

	pages := []struct {
		path   string
		status int
		en, de []string
	}{
		{"/", 200, []string{`lang="en"`, "Start reviewing", `data-lang="de"`}, []string{`lang="de"`, "Feedback starten", `data-lang="en"`}},
		{"/edit/" + key, 200, []string{"Apply and get the link", `id="pp-text">{}`}, []string{"Absenden und Link erhalten", "Feedback zu example.com", `"Copied":"Kopiert"`}},
		{"/r/" + rv.Code, 200, []string{"1 annotation", "mobile (390px)", "Bug"}, []string{"1 Anmerkung", "Smartphone (390px)", "Fehler", "Schaltfläche", "von Sam", "gesendet am"}},
		{"/no/such/page", 404, []string{"Nothing here"}, []string{"Hier ist nichts"}},
		{"/edit/wrong", 404, []string{"This review isn&#39;t here"}, []string{"Dieses Feedback gibt es hier nicht"}},
	}
	for _, p := range pages {
		for lang, want := range map[string][]string{"en-GB": p.en, "de-DE": p.de} {
			status, body, h := get(p.path, lang)
			if status != p.status {
				t.Errorf("%s in %s: status %d, want %d", p.path, lang, status, p.status)
			}
			for _, w := range want {
				if !strings.Contains(body, w) {
					t.Errorf("%s in %s does not say %q", p.path, lang, w)
				}
			}
			if vary := strings.Join(h.Values("Vary"), ", "); !strings.Contains(vary, "Accept-Language") {
				t.Errorf("%s in %s: Vary is %q, so a cache could serve it in the wrong language", p.path, lang, vary)
			}
		}
	}

	// What a coding AI reads stays in English.
	_, md, _ := get("/r/"+rv.Code+".md", "de-DE")
	if !strings.Contains(md, "mobile (390px)") {
		t.Errorf("the Markdown lost its device:\n%s", md)
	}
	if !strings.Contains(md, "# Review of example.com") || strings.Contains(md, "Anmerkung") {
		t.Errorf("the Markdown changed language:\n%s", md)
	}
}
