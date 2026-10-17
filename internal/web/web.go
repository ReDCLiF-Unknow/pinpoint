// Package web serves PinPoint: the start page, the review editor, the shared
// review, and the proxy that shows the site being reviewed.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"pinpoint/internal/proxy"
	"pinpoint/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// manifestJSON makes PinPoint installable. There is no service worker, so it
// is not usable offline.
const manifestJSON = `{
  "name": "PinPoint",
  "short_name": "PinPoint",
  "description": "Mark up a website, and hand the feedback to a coding AI",
  "start_url": "/",
  "scope": "/",
  "display": "standalone",
  "background_color": "#151f2c",
  "theme_color": "#1d273b",
  "icons": [
    {"src": "/static/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any maskable"},
    {"src": "/static/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any maskable"}
  ]
}`

// labelInfo is how each label looks and reads.
type labelInfo struct{ Name, Title, Icon, Color string }

var labels = map[string]labelInfo{
	"change":   {"change", "Change", "pencil", "blue"},
	"bug":      {"bug", "Bug", "bug", "red"},
	"question": {"question", "Question", "help", "orange"},
	"like":     {"like", "Like it", "heart", "green"},
}

// kindInfo is how each kind of mark reads.
var kinds = map[string]labelInfo{
	"pin":       {"pin", "Comment", "message-circle", ""},
	"highlight": {"highlight", "Highlight", "highlight", ""},
	"box":       {"box", "Box", "square", ""},
	"draw":      {"draw", "Drawing", "scribble", ""},
}

var funcs = template.FuncMap{
	"label": func(l string) labelInfo {
		if v, ok := labels[l]; ok {
			return v
		}
		return labels["change"]
	},
	"kind": func(k string) labelInfo { return kinds[k] },
	"labels": func() []labelInfo {
		return []labelInfo{labels["change"], labels["bug"], labels["question"], labels["like"]}
	},
	"tools": func() []labelInfo {
		return []labelInfo{kinds["pin"], kinds["highlight"], kinds["box"], kinds["draw"]}
	},
	"date":   func(t time.Time) string { return t.Local().Format("2 January 2006") },
	"plural": plural,
	"json": func(v any) (template.JS, error) {
		b, err := json.Marshal(v)
		return template.JS(b), err
	},
	"add": func(a, b int) int { return a + b },
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}

type Server struct {
	store *store.Store
	proxy *proxy.Client
	tmpl  *template.Template
	mux   *http.ServeMux
	// creates limits how fast one address can start reviews, and fetches how
	// fast it can make the server fetch pages, as each one is a request to
	// somebody else's site.
	creates, fetches *limiter
	sameOrigin       *http.CrossOriginProtection
	allowPrivate     bool
	// proxies are the reverse proxies whose X-Forwarded-For says who the
	// visitor is, for the limits. None unless the operator names them.
	proxies []netip.Prefix
	// assetVersion changes whenever the page scripts do, so browsers fetch
	// them again after an upgrade rather than mixing old and new.
	assetVersion string
}

// Option changes how the server is set up.
type Option func(*Server)

// WithPrivateNetworks lets reviews show sites on private networks and on this
// machine. Only for a server nobody else can reach: otherwise anyone could
// use it to look inside the network it runs in.
func WithPrivateNetworks() Option { return func(s *Server) { s.allowPrivate = true } }

// WithTrustedProxies names the reverse proxies in front of the server, so the
// limits count each visitor rather than the proxy. Only list proxies that add
// to X-Forwarded-For themselves: anything that reaches the server directly
// from one of these addresses can claim to be anybody.
func WithTrustedProxies(p []netip.Prefix) Option { return func(s *Server) { s.proxies = p } }

// New makes the web app.
func New(s *store.Store, opts ...Option) *Server {
	srv := &Server{
		store:      s,
		tmpl:       template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")),
		mux:        http.NewServeMux(),
		creates:    newLimiter(20, time.Minute),
		fetches:    newLimiter(300, 200*time.Millisecond),
		sameOrigin: http.NewCrossOriginProtection(),
	}
	for _, o := range opts {
		o(srv)
	}
	srv.proxy = proxy.New(srv.allowPrivate)
	srv.assetVersion = versionOf("static/agent.js", "static/app.js", "static/vendor/modern-screenshot.js")
	srv.routes()
	return srv
}

// versionOf is a short hash of some embedded files.
func versionOf(names ...string) string {
	h := sha256.New()
	for _, n := range names {
		b, err := staticFS.ReadFile(n)
		if err != nil {
			panic(err)
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}

// csp keeps PinPoint's own pages to their own origin. The review frame is
// served with a policy of its own (see frame.go).
const csp = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; frame-src 'self'; form-action 'self'; " +
	"frame-ancestors 'none'; base-uri 'none'"

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if err := s.sameOrigin.Check(r); err != nil {
			http.Error(w, "This request came from another site.", http.StatusForbidden)
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /{$}", s.startPage)
	m.HandleFunc("POST /reviews", s.limited(s.creates, s.createReview, s.tooManyReviews))

	m.HandleFunc("GET /edit/{key}", s.editorPage)
	m.HandleFunc("POST /edit/{key}/annotations", s.apiAdd)
	m.HandleFunc("POST /edit/{key}/annotations/{id}", s.apiUpdate)
	m.HandleFunc("POST /edit/{key}/annotations/{id}/delete", s.apiDelete)
	m.HandleFunc("POST /edit/{key}/annotations/{id}/shot", s.apiShot)
	m.HandleFunc("POST /edit/{key}/apply", s.apiApply)

	m.HandleFunc("GET /r/{code}", s.reviewPage)
	m.HandleFunc("GET /r/{code}/{shot}", s.shot)

	m.HandleFunc("GET /f/{key}", s.limited(s.fetches, s.frame, s.tooManyFetches))
	m.HandleFunc("GET /x/{key}", s.limited(s.fetches, s.resource, s.tooManyFetches))

	static, _ := fs.Sub(staticFS, "static")
	files := http.StripPrefix("/static/", http.FileServerFS(static))
	m.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The review script loads the screenshot code from inside the
		// frame, whose origin is nobody's. Nothing here is secret.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		// Links carry ?v=<hash of the scripts>, so a long cache is safe:
		// a new version is a new URL.
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		files.ServeHTTP(w, r)
	}))
	m.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		io.WriteString(w, manifestJSON)
	})
	m.HandleFunc("/", s.notFound)
}

// baseURL is the scheme and host this request reached PinPoint at.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var b strings.Builder
	if err := s.tmpl.ExecuteTemplate(&b, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	io.WriteString(w, b.String())
}

// messagePage is a page that only says something.
type messagePage struct {
	Title, Text string
	Icon        string
	Version     string
}

func (s *Server) message(w http.ResponseWriter, status int, title, text string) {
	s.render(w, status, "message.html", messagePage{Title: title, Text: text, Icon: "map-pin-off", Version: s.assetVersion})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	// A page in the review frame that went somewhere PinPoint does not know,
	// which is what reloading a page the review script has relabelled does:
	// tell the editor, which takes it back to the last page it saw.
	if r.Header.Get("Sec-Fetch-Dest") == "iframe" {
		w.Header().Set("Content-Security-Policy", frameCSP)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `<!doctype html><meta charset="utf-8"><script>parent.postMessage({pp:"lost"}, "*")</script>`)
		return
	}
	s.message(w, http.StatusNotFound, "Nothing here", "This link doesn't lead anywhere. If somebody sent it to you, ask them for it again.")
}

// ---- start ----------------------------------------------------------------

type startPage struct {
	URL     string
	Error   string
	Version string
}

func (s *Server) startPage(w http.ResponseWriter, r *http.Request) {
	// An agency's prepared link: /?url=https://client.example
	s.render(w, http.StatusOK, "start.html", startPage{URL: r.URL.Query().Get("url"), Version: s.assetVersion})
}

func (s *Server) tooManyReviews(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusTooManyRequests, "start.html", startPage{URL: r.FormValue("url"),
		Error: "That's a lot of reviews in a short time. Wait a minute, then try again.", Version: s.assetVersion})
}

func (s *Server) tooManyFetches(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", frameCSP)
	http.Error(w, "Too many pages in a short time. Wait a minute, then try again.", http.StatusTooManyRequests)
}

// siteURL makes sense of what somebody typed into the address field.
func siteURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("Type the address of the website to review.")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("That doesn't look like a website address. It should look like example.com.")
	}
	if !strings.Contains(u.Hostname(), ".") && u.Hostname() != "localhost" {
		return nil, errors.New("That doesn't look like a website address. It should look like example.com.")
	}
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u, nil
}

func (s *Server) createReview(w http.ResponseWriter, r *http.Request) {
	raw := r.FormValue("url")
	fail := func(msg string) {
		s.render(w, http.StatusUnprocessableEntity, "start.html", startPage{URL: raw, Error: msg, Version: s.assetVersion})
	}
	u, err := siteURL(raw)
	if err != nil {
		fail(err.Error())
		return
	}
	// Look at it first: a typo is better caught here than as an empty frame.
	resp, err := s.proxy.Get(r.Context(), u, r.Header)
	if err != nil {
		fail(proxy.Explain(err))
		return
	}
	defer resp.Body.Close()
	// Where it ended up is the site: example.com that redirects to
	// www.example.com is reviewed as www.example.com.
	final := resp.Request.URL
	title := ""
	if isHTML(resp.Header.Get("Content-Type")) {
		if body, err := proxy.ReadAll(resp.Body, maxPage); err == nil {
			title = proxy.Title(proxy.Decode(body, resp.Header.Get("Content-Type")))
		}
	}
	rv, key, err := s.store.CreateReview(final.String(), final.Host, title)
	if err != nil {
		log.Print(err)
		fail("Something went wrong. Try again.")
		return
	}
	log.Printf("review %d started for %s", rv.ID, rv.SiteHost)
	http.Redirect(w, r, "/edit/"+key, http.StatusSeeOther)
}

func isHTML(contentType string) bool {
	ct := strings.ToLower(contentType)
	return ct == "" || strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "application/xhtml")
}
