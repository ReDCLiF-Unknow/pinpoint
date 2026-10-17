package web

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// limiter is a token bucket per client address.
type limiter struct {
	mu      sync.Mutex
	burst   float64
	refill  time.Duration
	buckets map[string]*bucket
	now     func() time.Time // replaced in tests
}

type bucket struct {
	tokens float64
	seen   time.Time
}

func newLimiter(burst int, refill time.Duration) *limiter {
	return &limiter{
		burst:   float64(burst),
		refill:  refill,
		buckets: map[string]*bucket{},
		now:     time.Now,
	}
}

// allow reports whether this address may do the thing now, and takes a token
// if so.
func (l *limiter) allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, seen: now}
		l.buckets[key] = b
		l.sweep(now)
	}
	// Hand back one token per refill period since we last looked.
	b.tokens += now.Sub(b.seen).Seconds() / l.refill.Seconds()
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.seen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep forgets addresses whose buckets have long since refilled, so a busy
// server does not accumulate one entry per address that ever visited. Called
// with the lock held, and only when adding an address.
func (l *limiter) sweep(now time.Time) {
	if len(l.buckets) < 1000 {
		return
	}
	stale := time.Duration(l.burst) * l.refill
	for key, b := range l.buckets {
		if now.Sub(b.seen) > stale {
			delete(l.buckets, key)
		}
	}
}

// ParseTrustedProxies reads a comma-separated list of addresses and ranges,
// such as "10.0.0.0/8, ::1", for WithTrustedProxies. An empty list is no
// proxies at all.
func ParseTrustedProxies(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			p, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, fmt.Errorf("trusted proxy %q is not an address or a range like 10.0.0.0/8", item)
			}
			// Addresses are compared as plain IPv4 where they can be, so an
			// IPv4 range written the IPv6 way has to be as well.
			if p.Addr().Is4In6() && p.Bits() >= 96 {
				p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil || a.Zone() != "" {
			return nil, fmt.Errorf("trusted proxy %q is not an address or a range like 10.0.0.0/8", item)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// clientIP is who to count against. Normally that is whoever connected:
// X-Forwarded-For is not believed, because anyone can send it and a limit you
// can opt out of is not a limit. Behind a shared proxy the whole site then
// counts as one caller, which is the safe way round.
//
// When the connection comes from a trusted proxy, the header is believed only
// as far as trusted proxies wrote it. Each proxy adds the address it heard
// from to the right-hand end, so reading from the right and skipping proxies
// stops at the first address none of them can vouch past: the visitor.
// Anything further left came from the visitor and could say anything.
func clientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !isTrusted(peer, trusted) {
		return host
	}
	client := peer
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := parseHop(hops[i])
		if !ok {
			// A proxy we trust passed on something that is not an address.
			// Count the last one we could read rather than guess.
			break
		}
		client = hop
		if !isTrusted(hop, trusted) {
			break
		}
	}
	return client.String()
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	a = a.Unmap().WithZone("")
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// parseHop reads one address from X-Forwarded-For. Some proxies write the
// port as well, which says nothing about who the visitor is.
func parseHop(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	a, err := netip.ParseAddr(s)
	if err != nil {
		ap, err := netip.ParseAddrPort(s)
		if err != nil {
			return netip.Addr{}, false
		}
		a = ap.Addr()
	}
	return a.Unmap().WithZone(""), true
}

// limited wraps a handler so that one address cannot call it endlessly.
// denied says what to do about the ones that are turned away.
func (s *Server) limited(l *limiter, h http.HandlerFunc, denied http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r, s.proxies)) {
			w.Header().Set("Retry-After", "60")
			denied(w, r)
			return
		}
		h(w, r)
	}
}
