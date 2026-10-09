// Package proxy fetches the pages a review frame shows.
//
// Most sites refuse to be shown in a frame on another site, so PinPoint
// fetches each page itself and serves it from its own address, without the
// headers that refuse framing, and with the review script added. Only the
// page itself comes through here: a <base> element points everything it
// loads (styles, scripts, images) back at the real site, so the browser loads
// those directly, as it would without PinPoint.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrPrivate is an address on a private network, which a server on the
	// internet must not be made to reach for strangers.
	ErrPrivate = errors.New("that address is on a private network")
	// ErrTooBig is a response larger than the proxy passes on.
	ErrTooBig = errors.New("the response is too large")
)

// Client fetches pages from the sites being reviewed.
type Client struct {
	http *http.Client
}

// New makes a client. Unless allowPrivate is set, it refuses to connect to
// loopback, private, link-local and other non-public addresses, checked on
// the address it actually connects to, so a public name that resolves to
// 127.0.0.1 is refused too.
func New(allowPrivate bool) *Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		dialer.Control = func(network, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return ErrPrivate
			}
			if !Public(ap.Addr()) {
				return ErrPrivate
			}
			return nil
		}
	}
	transport := &http.Transport{
		// Never through an HTTP_PROXY from the environment: the check above
		// would then only ever see the proxy's address.
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          50,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
	return &Client{http: &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("a redirect to %s", req.URL.Scheme)
			}
			return nil
		},
		// No cookie jar: a review never signs in as anybody.
	}}
}

// blocked are the ranges no public site lives in, beyond the ones netip
// already knows about.
var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"), // NAT64, which can reach IPv4 private ranges
	netip.MustParsePrefix("2001:db8::/32"),
}

// Public reports whether addr is an address on the public internet.
func Public(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() || addr.IsUnspecified() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() ||
		addr.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range blocked {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// Get fetches u. Only a few of the browser's headers are passed on: enough
// for the site to send the right language and layout, and nothing that says
// who is looking.
func (c *Client) Get(ctx context.Context, u *url.URL, from http.Header) (*http.Response, error) {
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("not a web address: %s", u.Scheme)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	for _, h := range []string{"User-Agent", "Accept", "Accept-Language"} {
		if v := from.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; PinPoint review)")
	}
	return c.http.Do(req)
}

// ReadAll reads at most limit bytes of r, and ErrTooBig if there is more.
func ReadAll(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, ErrTooBig
	}
	return b, nil
}

// Explain turns a fetch error into something to tell the person who typed the
// address.
func Explain(err error) string {
	var dns *net.DNSError
	switch {
	case errors.Is(err, ErrPrivate):
		return "That address is on a private network, which PinPoint can't reach. Staging and local sites need the script snippet."
	case errors.As(err, &dns):
		return "There's no website at that address. Check it for typos."
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Timeout"):
		return "The website took too long to answer."
	case errors.Is(err, ErrTooBig):
		return "That page is too large to review."
	case strings.Contains(err.Error(), "certificate"):
		return "The website's security certificate isn't valid."
	case strings.Contains(err.Error(), "redirect"):
		return "The website keeps redirecting and never arrives at a page."
	}
	return "Couldn't reach the website."
}

// SameSite reports whether host belongs to the site being reviewed, which
// for now means the same host, give or take a leading "www.".
func SameSite(host, site string) bool {
	trim := func(h string) string { return strings.TrimPrefix(strings.ToLower(h), "www.") }
	return host != "" && trim(host) == trim(site)
}
