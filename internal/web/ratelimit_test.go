package web

import (
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
