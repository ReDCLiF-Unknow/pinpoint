package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"pinpoint/internal/store"
)

// serveTypes answers each path with a body and the content type it is given.
func serveTypes(types map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct, ok := types[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ct)
		w.Write([]byte("the file at " + r.URL.Path))
	}))
}

func TestResourceFromOtherSitesIsOnlyWhatAScreenshotNeeds(t *testing.T) {
	other := serveTypes(map[string]string{
		"/logo.png":        "image/png",
		"/icon.svg":        "image/svg+xml",
		"/font.woff2":      "font/woff2",
		"/old.woff":        "application/x-font-woff",
		"/cdn/font.woff2":  "application/octet-stream",
		"/style.css":       "text/css; charset=utf-8",
		"/api.json":        "application/json",
		"/page":            "text/html; charset=utf-8",
		"/script.js":       "text/javascript",
		"/blob.bin":        "application/octet-stream",
		"/untyped":         "",
		"/bad-type":        "not a type",
		"/sneaky.woff.txt": "text/plain",
	})
	defer other.Close()

	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data.json":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"open":"9-5"}`))
		case "/away":
			http.Redirect(w, r, other.URL+"/api.json", http.StatusFound)
		case "/to-logo":
			http.Redirect(w, r, other.URL+"/logo.png", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()

	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	siteURL, _ := url.Parse(site.URL)
	rv, _, err := st.CreateReview(site.URL+"/", siteURL.Host, "Site")
	if err != nil {
		t.Fatal(err)
	}
	srv := New(st, WithPrivateNetworks())

	for target, want := range map[string]int{
		// The site's own files, whatever they are.
		site.URL + "/data.json": http.StatusOK,
		// Another site's images, fonts and stylesheets.
		other.URL + "/logo.png":       http.StatusOK,
		other.URL + "/icon.svg":       http.StatusOK,
		other.URL + "/font.woff2":     http.StatusOK,
		other.URL + "/old.woff":       http.StatusOK,
		other.URL + "/cdn/font.woff2": http.StatusOK,
		other.URL + "/style.css":      http.StatusOK,
		site.URL + "/to-logo":         http.StatusOK,
		// And nothing else of theirs.
		other.URL + "/api.json":        http.StatusForbidden,
		other.URL + "/page":            http.StatusForbidden,
		other.URL + "/script.js":       http.StatusForbidden,
		other.URL + "/blob.bin":        http.StatusForbidden,
		other.URL + "/untyped":         http.StatusForbidden,
		other.URL + "/bad-type":        http.StatusForbidden,
		other.URL + "/sneaky.woff.txt": http.StatusForbidden,
		// The site's address is no way round it.
		site.URL + "/away": http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodGet, "/x/"+rv.FrameKey+"?url="+url.QueryEscape(target), nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: status %d, want %d (%s)", target, rec.Code, want, rec.Body.String())
		}
	}
}

func TestResourceNeedsAReview(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := New(st, WithPrivateNetworks())
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x/nope?url="+url.QueryEscape("https://example.com/logo.png"), nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("an unknown key got status %d, want 404", rec.Code)
	}
}
