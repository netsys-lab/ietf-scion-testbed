package ratelimit

import (
	"testing"
	"time"
)

func TestAllowUnderLimitThenBlocks(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(5, time.Minute, func() time.Time { return now })
	for i := 0; i < 5; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("hit %d should be allowed", i)
		}
	}
	if l.Allow("1.2.3.4") {
		t.Fatal("6th hit within the window must be blocked")
	}
}

func TestWindowSlides(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(1, time.Minute, func() time.Time { return now })
	if !l.Allow("1.2.3.4") {
		t.Fatal("first hit allowed")
	}
	if l.Allow("1.2.3.4") {
		t.Fatal("second hit blocked within window")
	}
	now = now.Add(61 * time.Second)
	if !l.Allow("1.2.3.4") {
		t.Fatal("hit after the window elapses must be allowed")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(1, time.Minute, func() time.Time { return now })
	if !l.Allow("a") || !l.Allow("b") {
		t.Fatal("distinct keys have independent budgets")
	}
	if l.Allow("a") {
		t.Fatal("key a is now exhausted")
	}
}

func TestClientKeyV4IsFullAddress(t *testing.T) {
	if got := ClientKey("203.0.113.5:44321"); got != "203.0.113.5" {
		t.Fatalf("v4 key = %q", got)
	}
}

func TestClientKeyV6IsSlash64(t *testing.T) {
	a := ClientKey("[2001:db8:abcd:0012::1]:5")
	b := ClientKey("[2001:db8:abcd:0012:ffff:ffff:ffff:ffff]:9")
	c := ClientKey("[2001:db8:abcd:0013::1]:5")
	if a != b {
		t.Fatalf("same /64 should share a key: %q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("different /64 must differ: %q vs %q", a, c)
	}
}
