package hev3

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ctl controls one fake dial attempt, keyed by Candidate.Label. Every field
// mutated by the dial goroutine (startedAt, ctxCancelled) is published to the
// test through a channel close, giving a happens-before edge the race
// detector accepts.
type ctl struct {
	est   *Established
	err   error
	block bool // if set, the dial blocks until release closes or ctx is done

	started      chan struct{} // closed when dial begins
	startedAt    time.Time
	release      chan struct{} // close to unblock a blocking dial
	ctxCancelled chan struct{} // closed if a blocking dial observes ctx.Done
	ignoreCtx    bool          // block on release only, never on ctx (straggler sim)
}

func newCtl() *ctl {
	return &ctl{
		started:      make(chan struct{}),
		release:      make(chan struct{}),
		ctxCancelled: make(chan struct{}),
	}
}

// fakeDialer builds a DialFunc backed by a per-label ctl map.
func fakeDialer(ctls map[string]*ctl) DialFunc {
	return func(ctx context.Context, c Candidate) (*Established, error) {
		k := ctls[c.Label]
		k.startedAt = time.Now()
		close(k.started)
		if !k.block {
			return k.est, k.err
		}
		if k.ignoreCtx {
			<-k.release
			return k.est, k.err
		}
		select {
		case <-k.release:
			return k.est, k.err
		case <-ctx.Done():
			close(k.ctxCancelled)
			return nil, ctx.Err()
		}
	}
}

func cand(label string) Candidate {
	return Candidate{Family: FamilyIPv4, Host: "203.0.113.1", Label: label}
}

func fakeEstablished(c Candidate) *Established {
	return &Established{Cand: c, RT: http.DefaultTransport, Close: func() error { return nil }, ALPN: "h2"}
}

func waitClosed(t *testing.T, ch <-chan struct{}, within time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(within):
		t.Fatalf("timed out after %v waiting for %s", within, what)
	}
}

func labelsOf(evs []Event, kind string) map[string]bool {
	out := map[string]bool{}
	for _, e := range evs {
		if e.Kind == kind {
			out[e.Label] = true
		}
	}
	return out
}

// (a) first success cancels every other in-flight attempt: their ctx fires and
// a cancel event is recorded for each; a winner event names the winner.
func TestRace_FirstSuccessCancelsInflight(t *testing.T) {
	c0, c1, c2 := cand("a"), cand("b"), cand("c")
	ctls := map[string]*ctl{"a": newCtl(), "b": newCtl(), "c": newCtl()}
	for _, c := range ctls {
		c.block = true
	}
	ctls["b"].est = fakeEstablished(c1)

	var tl Timeline
	done := make(chan struct{})
	var got *Established
	var gotErr error
	go func() {
		got, gotErr = Race(context.Background(), []Candidate{c0, c1, c2},
			fakeDialer(ctls), RaceOptions{AttemptDelay: 20 * time.Millisecond, Timeline: &tl})
		close(done)
	}()

	// All three must be in flight before we pick a winner.
	for _, k := range []string{"a", "b", "c"} {
		waitClosed(t, ctls[k].started, time.Second, "start of "+k)
	}
	close(ctls["b"].release) // b wins

	waitClosed(t, done, time.Second, "Race to return")
	if gotErr != nil {
		t.Fatalf("Race returned error: %v", gotErr)
	}
	if got == nil || got.Cand.Label != "b" {
		t.Fatalf("winner = %+v, want candidate b", got)
	}
	// Losers were cancelled: their dial ctx fired.
	waitClosed(t, ctls["a"].ctxCancelled, time.Second, "a ctx cancel")
	waitClosed(t, ctls["c"].ctxCancelled, time.Second, "c ctx cancel")

	evs := tl.Events()
	cancels := labelsOf(evs, "cancel")
	if !cancels["a"] || !cancels["c"] {
		t.Fatalf("missing cancel events, got %v", cancels)
	}
	if !labelsOf(evs, "winner")["b"] {
		t.Fatalf("missing winner event for b, events=%+v", evs)
	}
}

// (b) attempts are staggered: candidate i is not dialed before i*AttemptDelay.
func TestRace_Stagger(t *testing.T) {
	delay := 20 * time.Millisecond
	c0, c1, c2 := cand("a"), cand("b"), cand("c")
	ctls := map[string]*ctl{"a": newCtl(), "b": newCtl(), "c": newCtl()}
	for _, c := range ctls {
		c.block = true // none complete; only the stagger timer launches them
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		Race(ctx, []Candidate{c0, c1, c2}, fakeDialer(ctls), RaceOptions{AttemptDelay: delay})
		close(done)
	}()

	for _, k := range []string{"a", "b", "c"} {
		waitClosed(t, ctls[k].started, time.Second, "start of "+k)
	}
	base := ctls["a"].startedAt
	// Lower bounds only (a timer never fires early); slack for the base read.
	if d := ctls["b"].startedAt.Sub(base); d < delay-5*time.Millisecond {
		t.Fatalf("b started %v after a, want >= ~%v", d, delay)
	}
	if d := ctls["c"].startedAt.Sub(base); d < 2*delay-5*time.Millisecond {
		t.Fatalf("c started %v after a, want >= ~%v", d, 2*delay)
	}
	cancel()
	waitClosed(t, done, time.Second, "Race to return")
}

// (c) when the most-recent attempt fails, the next launches immediately rather
// than waiting out the (here deliberately huge) stagger.
func TestRace_EarlyPromotion(t *testing.T) {
	delay := 500 * time.Millisecond
	c0, c1 := cand("a"), cand("b")
	ctls := map[string]*ctl{"a": newCtl(), "b": newCtl()}
	ctls["a"].err = errors.New("boom") // fails at once
	ctls["b"].block = true
	ctls["b"].est = fakeEstablished(c1)

	done := make(chan struct{})
	var got *Established
	go func() {
		got, _ = Race(context.Background(), []Candidate{c0, c1}, fakeDialer(ctls),
			RaceOptions{AttemptDelay: delay})
		close(done)
	}()

	waitClosed(t, ctls["b"].started, time.Second, "start of b")
	gap := ctls["b"].startedAt.Sub(ctls["a"].startedAt)
	if gap >= 100*time.Millisecond {
		t.Fatalf("b started %v after a; promotion should be near-immediate, not the %v stagger", gap, delay)
	}
	close(ctls["b"].release)
	waitClosed(t, done, time.Second, "Race to return")
	if got == nil || got.Cand.Label != "b" {
		t.Fatalf("winner = %+v, want b", got)
	}
}

// (d) all-fail: the returned error names every candidate and wraps each cause.
func TestRace_AllFail(t *testing.T) {
	errA := errors.New("err-a")
	errB := errors.New("err-b")
	errC := errors.New("err-c")
	c0, c1, c2 := cand("a"), cand("b"), cand("c")
	ctls := map[string]*ctl{"a": newCtl(), "b": newCtl(), "c": newCtl()}
	ctls["a"].err, ctls["b"].err, ctls["c"].err = errA, errB, errC

	_, err := Race(context.Background(), []Candidate{c0, c1, c2}, fakeDialer(ctls), RaceOptions{})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	for _, lbl := range []string{"a", "b", "c"} {
		if !strings.Contains(err.Error(), lbl) {
			t.Fatalf("error %q missing label %q", err.Error(), lbl)
		}
	}
	for _, cause := range []error{errA, errB, errC} {
		if !errors.Is(err, cause) {
			t.Fatalf("error does not wrap %v", cause)
		}
	}
}

// (e) parent ctx cancellation aborts the race and returns ctx.Err().
func TestRace_CtxCancel(t *testing.T) {
	c0, c1 := cand("a"), cand("b")
	ctls := map[string]*ctl{"a": newCtl(), "b": newCtl()}
	ctls["a"].block, ctls["b"].block = true, true

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var err error
	go func() {
		_, err = Race(ctx, []Candidate{c0, c1}, fakeDialer(ctls), RaceOptions{AttemptDelay: 20 * time.Millisecond})
		close(done)
	}()

	waitClosed(t, ctls["a"].started, time.Second, "start of a")
	cancel()
	waitClosed(t, done, time.Second, "Race to return")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	waitClosed(t, ctls["a"].ctxCancelled, time.Second, "a ctx cancel")
}

// (f) an empty candidate list errors immediately.
func TestRace_Empty(t *testing.T) {
	got, err := Race(context.Background(), nil, fakeDialer(nil), RaceOptions{})
	if err == nil {
		t.Fatal("want error for empty candidates, got nil")
	}
	if got != nil {
		t.Fatalf("want nil Established, got %+v", got)
	}
}

// A straggler that completes successfully AFTER the winner was chosen — despite
// its cancellation — must be Closed, not leaked (draft §6).
func TestRace_StragglerClosed(t *testing.T) {
	c0, c1 := cand("a"), cand("b")
	ctls := map[string]*ctl{"a": newCtl(), "b": newCtl()}

	closed := make(chan struct{})
	stragglerEst := fakeEstablished(c0)
	stragglerEst.Close = func() error { close(closed); return nil }

	// a: launched first, ignores cancellation, later returns success.
	ctls["a"].block, ctls["a"].ignoreCtx = true, true
	ctls["a"].est = stragglerEst
	// b: launched after the stagger and wins immediately.
	ctls["b"].est = fakeEstablished(c1)

	got, err := Race(context.Background(), []Candidate{c0, c1}, fakeDialer(ctls),
		RaceOptions{AttemptDelay: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("Race error: %v", err)
	}
	if got == nil || got.Cand.Label != "b" {
		t.Fatalf("winner = %+v, want b", got)
	}
	// Now let the straggler finish; the drainer must Close it.
	close(ctls["a"].release)
	waitClosed(t, closed, time.Second, "straggler Close")
}
