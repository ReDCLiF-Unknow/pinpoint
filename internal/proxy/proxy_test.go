package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestPublic(t *testing.T) {
	for addr, want := range map[string]bool{
		"93.184.216.34":         true,
		"2606:2800:220:1::1":    true,
		"127.0.0.1":             false,
		"10.1.2.3":              false,
		"172.16.0.1":            false,
		"192.168.178.1":         false,
		"169.254.169.254":       false, // cloud metadata
		"100.64.0.1":            false,
		"0.0.0.0":               false,
		"::1":                   false,
		"fe80::1":               false,
		"fd00::1":               false,
		"::ffff:127.0.0.1":      false,
		"64:ff9b::a00:1":        false,
		"::ffff:93.184.216.34":  true,
		"224.0.0.1":             false,
		"255.255.255.255":       false,
		"2001:db8::1":           false,
		"198.18.0.1":            false,
		"192.0.0.8":             false,
		"8.8.8.8":               true,
		"2a00:1450:4001:80b::1": true,
	} {
		if got := Public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("Public(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret"))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	_, err := New(false).Get(context.Background(), u, http.Header{})
	if !errors.Is(err, ErrPrivate) {
		t.Fatalf("fetching %s: got %v, want ErrPrivate", u, err)
	}
	if !strings.Contains(Explain(err), "private network") {
		t.Errorf("explained as %q", Explain(err))
	}

	resp, err := New(true).Get(context.Background(), u, http.Header{})
	if err != nil {
		t.Fatalf("with private addresses allowed: %v", err)
	}
	resp.Body.Close()
}

// A public site must not be able to redirect the proxy into the network it
// runs in.
func TestRefusesRedirectsToPrivateAddresses(t *testing.T) {
	inside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret"))
	}))
	defer inside.Close()
	c := New(false)
	// The redirect itself comes from a test server, which is private too, so
	// check the redirect policy alone: the dialer is what refuses the target.
	req, _ := http.NewRequest("GET", inside.URL, nil)
	if err := c.http.CheckRedirect(req, nil); err != nil {
		t.Fatalf("a redirect to http was refused: %v", err)
	}
	ftp, _ := http.NewRequest("GET", "ftp://example.com/", nil)
	if err := c.http.CheckRedirect(ftp, nil); err == nil {
		t.Error("a redirect to ftp was allowed")
	}
	_, err := c.http.Get(inside.URL)
	if !errors.Is(err, ErrPrivate) {
		t.Errorf("got %v", err)
	}
}

func TestPassesOnlySomeHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	from := http.Header{}
	from.Set("Accept-Language", "de-DE")
	from.Set("User-Agent", "Firefox")
	from.Set("Cookie", "pinpoint=secret")
	from.Set("Referer", "https://pinpoint.example/edit/key")
	resp, err := New(true).Get(context.Background(), u, from)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got.Get("Accept-Language") != "de-DE" || got.Get("User-Agent") != "Firefox" {
		t.Errorf("lost headers: %v", got)
	}
	if got.Get("Cookie") != "" || got.Get("Referer") != "" {
		t.Errorf("passed on cookie or referer: %v", got)
	}
}

func TestReadAll(t *testing.T) {
	if b, err := ReadAll(strings.NewReader("12345"), 5); err != nil || string(b) != "12345" {
		t.Errorf("at the limit: %q %v", b, err)
	}
	if _, err := ReadAll(strings.NewReader("123456"), 5); !errors.Is(err, ErrTooBig) {
		t.Errorf("over the limit: %v", err)
	}
}

func TestSameSite(t *testing.T) {
	for _, c := range []struct {
		host, site string
		want       bool
	}{
		{"example.com", "example.com", true},
		{"www.example.com", "example.com", true},
		{"example.com", "www.example.com", true},
		{"EXAMPLE.com", "example.com", true},
		{"shop.example.com", "example.com", false},
		{"example.com.evil.net", "example.com", false},
		{"", "example.com", false},
		{"localhost:8095", "localhost:8095", true},
		{"localhost:8096", "localhost:8095", false},
	} {
		if got := SameSite(c.host, c.site); got != c.want {
			t.Errorf("SameSite(%q, %q) = %v", c.host, c.site, got)
		}
	}
}

func rewrite(t *testing.T, doc string) string {
	t.Helper()
	page, _ := url.Parse("https://example.com/shop/item?id=1")
	var b strings.Builder
	frame := func(u string) string { return "/f/KEY?url=" + url.QueryEscape(u) }
	if err := Rewrite(&b, []byte(doc), BaseURL([]byte(doc), page), "<script>AGENT</script>", frame); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestRewriteInjectsFirstInHead(t *testing.T) {
	got := rewrite(t, `<!doctype html>
<html lang="en"><head><title>Shop</title><script>site()</script></head><body><p>Hi</p></body></html>`)
	want := `<!doctype html>
<html lang="en"><head><base href="https://example.com/shop/item?id=1"><script>AGENT</script><title>Shop</title><script>site()</script></head><body><p>Hi</p></body></html>`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestRewriteWithoutHead(t *testing.T) {
	for _, doc := range []string{
		`<p>Just a paragraph</p>`,
		`<!-- hi --><html><body>x</body></html>`,
		`<title>t</title><p>x`,
		``,
	} {
		got := rewrite(t, doc)
		if !strings.Contains(got, `<script>AGENT</script>`) {
			t.Errorf("%q: no agent in %q", doc, got)
		}
		agent := strings.Index(got, "AGENT")
		for _, tag := range []string{"<p>", "<body>", "<title>"} {
			if i := strings.Index(got, tag); i >= 0 && i < agent {
				t.Errorf("%q: %s comes before the agent: %q", doc, tag, got)
			}
		}
	}
}

func TestRewriteUsesThePagesOwnBase(t *testing.T) {
	got := rewrite(t, `<html><head><meta charset="windows-1252"><base href="/assets/" target="_blank"><link rel=stylesheet href="a.css"></head></html>`)
	if !strings.Contains(got, `<base href="https://example.com/assets/">`) {
		t.Errorf("base: %s", got)
	}
	if strings.Count(got, "<base") != 1 || strings.Contains(got, "_blank") {
		t.Errorf("the page's own base is still there: %s", got)
	}
	if !strings.Contains(got, `<meta charset="utf-8">`) || strings.Contains(got, "1252") {
		t.Errorf("charset: %s", got)
	}
	if !strings.Contains(got, `<link rel=stylesheet href="a.css">`) {
		t.Errorf("other tags were changed: %s", got)
	}
}

func TestRewriteRemovesWhatWouldStopTheScript(t *testing.T) {
	got := rewrite(t, `<head><meta http-equiv="Content-Security-Policy" content="script-src 'self'">`+
		`<meta name="referrer" content="unsafe-url"><meta name="viewport" content="width=device-width"></head>`)
	if strings.Contains(got, "script-src") || strings.Contains(got, "unsafe-url") {
		t.Errorf("not removed: %s", got)
	}
	if !strings.Contains(got, `<meta name="viewport" content="width=device-width">`) {
		t.Errorf("viewport went too: %s", got)
	}
}

func TestRewriteSendsRefreshThroughTheFrame(t *testing.T) {
	got := rewrite(t, `<head><meta http-equiv="refresh" content="0; url=/new-home"></head>`)
	want := `content="0; url=/f/KEY?url=https%3A%2F%2Fexample.com%2Fnew-home"`
	if !strings.Contains(got, want) {
		t.Errorf("got %s, want %s", got, want)
	}
	// A refresh that only reloads stays as it is.
	got = rewrite(t, `<head><meta http-equiv="refresh" content="30"></head>`)
	if !strings.Contains(got, `content="30"`) {
		t.Errorf("reload refresh changed: %s", got)
	}
}

func TestRewriteLeavesScriptsAlone(t *testing.T) {
	doc := `<head></head><body><script>if (a < b && "<meta charset=x>") {}</script><textarea><base href=x></textarea></body>`
	got := rewrite(t, doc)
	if !strings.Contains(got, `<script>if (a < b && "<meta charset=x>") {}</script><textarea><base href=x></textarea>`) {
		t.Errorf("raw text was changed: %s", got)
	}
}

func TestDecode(t *testing.T) {
	latin1 := []byte("<p>Gr\xfc\xdfe</p>")
	if got := string(Decode(latin1, "text/html; charset=iso-8859-1")); got != "<p>Grüße</p>" {
		t.Errorf("got %q", got)
	}
	meta := []byte(`<meta charset="iso-8859-1"><p>Gr` + "\xfc\xdf" + `e</p>`)
	if got := string(Decode(meta, "text/html")); !strings.Contains(got, "Grüße") {
		t.Errorf("from meta: %q", got)
	}
}

func TestTitle(t *testing.T) {
	for doc, want := range map[string]string{
		`<html><head><title> Fish &amp;
		Chips </title></head>`: "Fish & Chips",
		`<title></title>`:                  "",
		`<body><title>late</title></body>`: "",
		`<svg><title>icon</title></svg>`:   "icon",
		`no markup`:                        "",
	} {
		if got := Title([]byte(doc)); got != want {
			t.Errorf("Title(%q) = %q, want %q", doc, got, want)
		}
	}
}
