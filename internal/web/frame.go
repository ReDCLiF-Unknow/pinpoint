package web

import (
	"encoding/json"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"pinpoint/internal/proxy"
	"pinpoint/internal/store"
)

const (
	// maxPage is the largest page the frame shows.
	maxPage = 8 << 20
	// maxResource is the largest image, font or file the page script fetches
	// through PinPoint, which it does for screenshots.
	maxResource = 15 << 20
)

// frameCSP is the policy for everything shown in the review frame.
//
// The page in the frame is somebody else's, served from PinPoint's own
// address, so its scripts must not get PinPoint's origin: "sandbox" without
// allow-same-origin gives the page an origin of its own that matches nothing,
// so it cannot read PinPoint's pages, cookies or storage, even if somebody
// opens its URL directly rather than in a frame. Forms are left out of the
// sandbox, so nothing a reviewer types is ever sent to the site. Popups may
// open (links to other sites open in a new tab, as themselves).
//
// The policy says nothing about where scripts and styles may come from: the
// page loads those from its own site, as it always does.
const frameCSP = "sandbox allow-scripts allow-popups allow-popups-to-escape-sandbox allow-modals; frame-ancestors 'self'"

func frameHeaders(h http.Header) {
	h.Set("Content-Security-Policy", frameCSP)
	// The page's own requests would otherwise tell the site (and anything it
	// loads, such as analytics) the address of the frame.
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
}

// frameURL is where the frame shows the page at target.
func frameURL(base, key, target string) string {
	return base + "/f/" + key + "?url=" + url.QueryEscape(target)
}

// agentConfig is what the review script in the frame is told about the page.
type agentConfig struct {
	Origin string `json:"origin"`
	Frame  string `json:"frame"`
	Page   string `json:"page"`
	Site   string `json:"site"`
	Status int    `json:"status"`
	// V is the version of PinPoint's scripts, for the screenshot code's URL.
	V string `json:"v"`
}

// frameMessage shows a message in the frame instead of a page, and tells the
// editor around it what happened.
func (s *Server) frameMessage(w http.ResponseWriter, r *http.Request, status int, title, text, link string) {
	frameHeaders(w.Header())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	note, _ := json.Marshal(map[string]string{"pp": "error", "title": title, "text": text, "link": link})
	base := baseURL(r)
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<link rel="stylesheet" href="` + base + `/static/vendor/tabler.min.css"><link rel="stylesheet" href="` + base + `/static/vendor/inter.css">`)
	b.WriteString(`<style>body{font-family:Inter,system-ui,sans-serif;display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;padding:1.5rem;background:#f6f8fb}` +
		`.box{max-width:30rem;text-align:center}@media (prefers-color-scheme:dark){body{background:#151f2c;color:#dce1e7}}</style></head><body><div class="box">`)
	b.WriteString(`<h2>` + html.EscapeString(title) + `</h2><p class="text-secondary">` + html.EscapeString(text) + `</p>`)
	if link != "" {
		b.WriteString(`<a class="btn" href="` + html.EscapeString(link) + `" target="_blank" rel="noopener">Open it in a new tab</a>`)
	}
	b.WriteString(`</div><script>parent.postMessage(` + string(note) + `, "*")</script></body></html>`)
	io.WriteString(w, b.String())
}

// frame shows a page of the site being reviewed, with the review script.
func (s *Server) frame(w http.ResponseWriter, r *http.Request) {
	rv, err := s.store.ReviewByFrameKey(r.PathValue("key"))
	if err != nil {
		s.frameMessage(w, r, http.StatusNotFound, "This review is gone", "The link may be wrong, or the review was deleted.", "")
		return
	}
	target, err := url.Parse(r.URL.Query().Get("url"))
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") {
		s.frameMessage(w, r, http.StatusBadRequest, "That isn't a web page", "Only web pages can be reviewed.", "")
		return
	}
	if !proxy.SameSite(target.Host, rv.SiteHost) {
		s.frameMessage(w, r, http.StatusForbidden, "That's another website",
			"This review is for "+rv.SiteHost+". Pages on other sites open in a new tab.", target.String())
		return
	}
	resp, err := s.proxy.Get(r.Context(), target, r.Header)
	if err != nil {
		s.frameMessage(w, r, http.StatusBadGateway, "Couldn't load the page", proxy.Explain(err), target.String())
		return
	}
	defer resp.Body.Close()

	final := resp.Request.URL
	if !proxy.SameSite(final.Host, rv.SiteHost) {
		s.frameMessage(w, r, http.StatusForbidden, "That page leads to another website",
			"It sends visitors on to "+final.Host+", which isn't part of this review.", final.String())
		return
	}
	frameHeaders(w.Header())
	ct := resp.Header.Get("Content-Type")
	if !isHTML(ct) {
		// An image or a PDF on its own: show it as it is.
		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, io.LimitReader(resp.Body, maxResource))
		return
	}
	body, err := proxy.ReadAll(resp.Body, maxPage)
	if err != nil {
		s.frameMessage(w, r, http.StatusBadGateway, "Couldn't load the page", proxy.Explain(err), final.String())
		return
	}
	doc := proxy.Decode(body, ct)
	origin := baseURL(r)
	cfg, _ := json.Marshal(agentConfig{Origin: origin, Frame: rv.FrameKey, Page: final.String(), Site: rv.SiteHost, Status: resp.StatusCode, V: s.assetVersion})
	inject := `<script>window.__pinpoint=` + string(cfg) + `</script><script src="` + origin + `/static/agent.js?v=` + s.assetVersion + `"></script>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(resp.StatusCode)
	err = proxy.Rewrite(w, doc, proxy.BaseURL(doc, final), inject, func(u string) string {
		return frameURL(origin, rv.FrameKey, u)
	})
	if err != nil {
		log.Printf("frame %s: %v", final, err)
	}
}

// resource fetches something a page in the frame needs from PinPoint rather
// than from its own site: an image or a font for a screenshot (which has to
// be read, and a browser only lets a page read what the other site allows),
// or data the page's own scripts ask its site for.
func (s *Server) resource(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	// What comes back is somebody else's file. It is never a page of
	// PinPoint's: sandboxed, and readable by the frame, whose origin is
	// nobody's.
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	if _, err := s.store.ReviewByFrameKey(r.PathValue("key")); err != nil {
		http.Error(w, "unknown review", http.StatusNotFound)
		return
	}
	target, err := url.Parse(r.URL.Query().Get("url"))
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") {
		http.Error(w, "not a web address", http.StatusBadRequest)
		return
	}
	resp, err := s.proxy.Get(r.Context(), target, r.Header)
	if err != nil {
		http.Error(w, proxy.Explain(err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, err := proxy.ReadAll(resp.Body, maxResource)
	if err != nil {
		http.Error(w, proxy.Explain(err), http.StatusBadGateway)
		return
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		h.Set("Content-Type", ct)
	}
	h.Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// reviewFrame is what the editor and the shared review need to show a site.
type reviewFrame struct {
	// Frame is the start of every frame URL; the page's address follows.
	Frame string `json:"frame"`
	Site  string `json:"site"`
	Host  string `json:"host"`
}

func newReviewFrame(r *http.Request, rv store.Review) reviewFrame {
	return reviewFrame{Frame: baseURL(r) + "/f/" + rv.FrameKey + "?url=", Site: rv.SiteURL, Host: rv.SiteHost}
}
