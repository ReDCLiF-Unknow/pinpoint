package web

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestLimiterRefillsOverTime(t *testing.T) {
	clock := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	l := newLimiter(3, time.Minute)
	l.now = func() time.Time { return clock }

	for i := 1; i <= 3; i++ {
		if !l.allow("10.0.0.1") {
			t.Fatalf("attempt %d of the burst was refused", i)
		}
	}
	if l.allow("10.0.0.1") {
		t.Error("a fourth attempt got through the burst")
	}
	// Somebody else is unaffected.
	if !l.allow("10.0.0.2") {
		t.Error("a different address was caught by someone else's limit")
	}

	// One token comes back per minute, and no more than the burst ever.
	clock = clock.Add(90 * time.Second)
	if !l.allow("10.0.0.1") {
		t.Error("nothing had refilled after a minute and a half")
	}
	if l.allow("10.0.0.1") {
		t.Error("more than one token came back in 90 seconds")
	}
	clock = clock.Add(time.Hour)
	for i := 1; i <= 3; i++ {
		if !l.allow("10.0.0.1") {
			t.Fatalf("after an hour, attempt %d was refused", i)
		}
	}
	if l.allow("10.0.0.1") {
		t.Error("an idle hour refilled past the burst")
	}
}

func TestSweepForgetsIdleAddresses(t *testing.T) {
	clock := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	l := newLimiter(2, time.Minute)
	l.now = func() time.Time { return clock }
	for i := 0; i < 1200; i++ {
		l.allow(strings.Repeat("a", i%50) + string(rune(i)))
	}
	before := len(l.buckets)
	clock = clock.Add(time.Hour)
	l.allow("someone-new")
	if len(l.buckets) >= before {
		t.Errorf("idle addresses were not forgotten: %d before, %d after", before, len(l.buckets))
	}
}

func TestClientIP(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8, 127.0.0.1, ::1, 2001:db8:1::/48")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		trusted []netip.Prefix
		remote  string
		xff     []string // one entry per X-Forwarded-For line
		want    string
	}{
		// With no proxies named, nothing changes from counting whoever
		// connected, however the header is dressed up.
		{"unset, no header", nil, "203.0.113.5:1234", nil, "203.0.113.5"},
		{"unset, header ignored", nil, "203.0.113.5:1234", []string{"198.51.100.7"}, "203.0.113.5"},
		{"unset, even from a proxy's address", nil, "10.0.0.1:1234", []string{"198.51.100.7"}, "10.0.0.1"},
		{"unset, no port", nil, "pipe", []string{"198.51.100.7"}, "pipe"},

		// A visitor connecting directly cannot pick their own bucket.
		{"untrusted peer spoofs", trusted, "203.0.113.5:1234", []string{"198.51.100.7"}, "203.0.113.5"},
		{"untrusted peer spoofs a proxy chain", trusted, "203.0.113.5:1234", []string{"198.51.100.7, 10.0.0.2"}, "203.0.113.5"},
		{"untrusted peer that is not an address", trusted, "localhost:80", []string{"198.51.100.7"}, "localhost"},

		{"one proxy", trusted, "10.0.0.1:1234", []string{"198.51.100.7"}, "198.51.100.7"},
		{"visitor's own claim is skipped", trusted, "10.0.0.1:1234", []string{"1.2.3.4, 198.51.100.7"}, "198.51.100.7"},
		{"several proxies", trusted, "10.0.0.1:1234", []string{"1.2.3.4, 198.51.100.7, 10.0.0.3, 127.0.0.1"}, "198.51.100.7"},
		{"several header lines", trusted, "10.0.0.1:1234", []string{"1.2.3.4", "198.51.100.7, 10.0.0.2"}, "198.51.100.7"},
		{"everyone is a proxy", trusted, "10.0.0.1:1234", []string{"10.0.0.9, 10.0.0.2"}, "10.0.0.9"},
		{"proxy sends no header", trusted, "10.0.0.1:1234", nil, "10.0.0.1"},
		{"proxy sends an empty header", trusted, "10.0.0.1:1234", []string{""}, "10.0.0.1"},
		{"spaces and tabs", trusted, "10.0.0.1:1234", []string{" 1.2.3.4 ,\t198.51.100.7\t"}, "198.51.100.7"},
		{"hop with a port", trusted, "10.0.0.1:1234", []string{"198.51.100.7:4711"}, "198.51.100.7"},

		// Rubbish stops the walk at the last address that could be read.
		{"rubbish last", trusted, "10.0.0.1:1234", []string{"198.51.100.7, rubbish"}, "10.0.0.1"},
		{"rubbish behind a proxy", trusted, "10.0.0.1:1234", []string{"rubbish, 10.0.0.2"}, "10.0.0.2"},
		{"unknown", trusted, "10.0.0.1:1234", []string{"unknown"}, "10.0.0.1"},
		{"empty entry", trusted, "10.0.0.1:1234", []string{"198.51.100.7,,10.0.0.2"}, "10.0.0.2"},
		{"rubbish beyond the visitor", trusted, "10.0.0.1:1234", []string{"rubbish, 198.51.100.7"}, "198.51.100.7"},
		{"a range is not an address", trusted, "10.0.0.1:1234", []string{"198.51.100.0/24"}, "10.0.0.1"},

		{"IPv6 proxy", trusted, "[2001:db8:1::5]:443", []string{"2001:db8:beef::1"}, "2001:db8:beef::1"},
		{"IPv6 loopback proxy", trusted, "[::1]:5000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"untrusted IPv6 peer spoofs", trusted, "[2001:db8:beef::1]:443", []string{"198.51.100.7"}, "2001:db8:beef::1"},
		{"IPv6 just outside the range", trusted, "[2001:db8:2::5]:443", []string{"198.51.100.7"}, "2001:db8:2::5"},
		{"mixed chain", trusted, "127.0.0.1:1234", []string{"2001:db8:beef::1, 2001:db8:1::9, 10.0.0.2"}, "2001:db8:beef::1"},
		{"IPv6 hop with a port", trusted, "10.0.0.1:1234", []string{"[2001:db8:beef::1]:443"}, "2001:db8:beef::1"},
		{"IPv4 proxy written as IPv6", trusted, "[::ffff:10.0.0.1]:1234", []string{"198.51.100.7"}, "198.51.100.7"},
		{"IPv4 visitor written as IPv6", trusted, "10.0.0.1:1234", []string{"::ffff:198.51.100.7"}, "198.51.100.7"},
		{"untrusted peer with a zone", trusted, "[fe80::1%eth0]:80", []string{"198.51.100.7"}, "fe80::1%eth0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remote
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := clientIP(r, tt.trusted); got != tt.want {
				t.Errorf("clientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseTrustedProxies(t *testing.T) {
	good := []struct {
		in   string
		want string
	}{
		{"", ""},
		{" , ", ""},
		{"10.0.0.0/8, 127.0.0.1 ,::1,2001:db8::/32", "10.0.0.0/8 127.0.0.1/32 ::1/128 2001:db8::/32"},
		{"10.1.2.3/8", "10.0.0.0/8"},
		{"::ffff:1.2.3.4", "1.2.3.4/32"},
		{"::ffff:10.0.0.0/104", "10.0.0.0/8"},
	}
	for _, tt := range good {
		got, err := ParseTrustedProxies(tt.in)
		if err != nil {
			t.Errorf("%q: %v", tt.in, err)
			continue
		}
		names := make([]string, len(got))
		for i, p := range got {
			names[i] = p.String()
		}
		if s := strings.Join(names, " "); s != tt.want {
			t.Errorf("%q read as %q, want %q", tt.in, s, tt.want)
		}
	}

	for _, in := range []string{"rubbish", "10.0.0", "10.0.0.0/33", "10.0.0.0/8/8", "1.2.3.4:80", "fe80::1%eth0", "10.0.0.1, rubbish"} {
		if got, err := ParseTrustedProxies(in); err == nil {
			t.Errorf("%q was accepted as %v", in, got)
		}
	}
}

// Behind a trusted proxy two visitors get a limit each, and one of them
// cannot reset theirs by making up a new address every time.
func TestLimitedBehindProxy(t *testing.T) {
	trusted, _ := ParseTrustedProxies("10.0.0.1")
	s := &Server{proxies: trusted}
	ok := func(w http.ResponseWriter, r *http.Request) {}
	denied := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTooManyRequests) }
	h := s.limited(newLimiter(2, time.Hour), ok, denied)

	call := func(xff string) int {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.1:1234"
		r.Header.Set("X-Forwarded-For", xff)
		w := httptest.NewRecorder()
		h(w, r)
		return w.Code
	}
	for i := 1; i <= 2; i++ {
		if code := call("1.2.3.4, 198.51.100.7"); code != http.StatusOK {
			t.Fatalf("attempt %d was refused", i)
		}
	}
	if code := call("5.6.7.8, 198.51.100.7"); code != http.StatusTooManyRequests {
		t.Error("a made-up address in front of the real one got past the limit")
	}
	if code := call("203.0.113.5"); code != http.StatusOK {
		t.Error("another visitor behind the same proxy was caught by the first one's limit")
	}
}
