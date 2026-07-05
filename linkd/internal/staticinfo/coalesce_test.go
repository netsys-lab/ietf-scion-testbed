package staticinfo

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestCoalesceLeadingAndTrailing(t *testing.T) {
	var n int32
	f := Coalesce(50*time.Millisecond, func() { atomic.AddInt32(&n, 1) })

	// A burst of rapid calls: the first must fire immediately (leading
	// edge), the rest must collapse into a single trailing call once the
	// window elapses.
	for i := 0; i < 20; i++ {
		f()
	}
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("immediately after burst: f ran %d times, want 1 (leading edge only)", got)
	}

	time.Sleep(150 * time.Millisecond)
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Fatalf("after window: f ran %d times, want 2 (leading + one trailing)", got)
	}
}

func TestCoalesceNoTrailingWithoutExtraCalls(t *testing.T) {
	var n int32
	f := Coalesce(50*time.Millisecond, func() { atomic.AddInt32(&n, 1) })

	f() // single call: leading edge only, nothing pending
	time.Sleep(150 * time.Millisecond)
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("f ran %d times, want 1 (no trailing call when nothing coalesced)", got)
	}
}

func TestCoalesceQuietPeriodFiresImmediatelyAgain(t *testing.T) {
	var n int32
	f := Coalesce(50*time.Millisecond, func() { atomic.AddInt32(&n, 1) })

	f()
	time.Sleep(150 * time.Millisecond) // well past the window: back to quiescent
	if got := atomic.LoadInt32(&n); got != 1 {
		t.Fatalf("f ran %d times, want 1", got)
	}

	f() // a call after the quiet period must fire immediately again
	if got := atomic.LoadInt32(&n); got != 2 {
		t.Fatalf("f ran %d times, want 2 (immediate leading edge after quiet period)", got)
	}
}
