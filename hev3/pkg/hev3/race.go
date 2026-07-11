package hev3

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// defaultAttemptDelay is the stagger between attempt launches when
// RaceOptions.AttemptDelay is left zero (draft-ietf-happy-happyeyeballs-v3-04
// §6 "Connection Attempt Delay", default 250ms).
const defaultAttemptDelay = 250 * time.Millisecond

// Established is a completed connection to one Candidate, ready for use.
type Established struct {
	Cand  Candidate
	RT    http.RoundTripper
	Close func() error
	ALPN  string
}

// DialFunc dials one Candidate. It must honour ctx cancellation and, on
// success, return a non-nil *Established whose Close releases the connection.
type DialFunc func(ctx context.Context, c Candidate) (*Established, error)

// RaceOptions tunes a Race. The zero value is valid.
type RaceOptions struct {
	AttemptDelay time.Duration // stagger between launches; 0 means defaultAttemptDelay
	Timeline     *Timeline     // optional; records attempt/success/fail/cancel/winner events
}

// Race dials cands concurrently per draft-ietf-happy-happyeyeballs-v3-04 §6:
// attempts start in list order staggered by AttemptDelay, a failing most-recent
// attempt promotes the next candidate immediately, and the first success wins —
// cancelling every other in-flight attempt and abandoning the unlaunched rest.
// The winner's *Established is returned; a straggler that succeeds anyway is
// closed. If all attempts fail, the returned error names every candidate; if
// ctx is cancelled, its error is returned.
func Race(ctx context.Context, cands []Candidate, dial DialFunc, o RaceOptions) (*Established, error) {
	if len(cands) == 0 {
		return nil, errors.New("hev3: no candidates to race")
	}
	delay := o.AttemptDelay
	if delay <= 0 {
		delay = defaultAttemptDelay
	}
	tl := o.Timeline

	// Buffered so every attempt goroutine can always deliver its result and
	// exit even on the paths where nobody is left reading (ctx cancellation).
	results := make(chan attemptResult, len(cands))

	cancels := make([]context.CancelFunc, len(cands)) // non-nil iff launched
	done := make([]bool, len(cands))                  // result received
	failErrs := make([]error, len(cands))

	var (
		next         int // index of the next candidate to launch
		inflight     int
		lastLaunched = -1 // most-recently launched index (drives early promotion)
	)

	launch := func(i int) {
		cctx, cancel := context.WithCancel(ctx)
		cancels[i] = cancel
		inflight++
		lastLaunched = i
		if tl != nil {
			tl.Add("attempt", cands[i].Label, "")
		}
		go func() {
			est, err := dial(cctx, cands[i])
			results <- attemptResult{i, est, err}
		}()
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	stopTimer := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}

	launch(0)
	next = 1

	for {
		select {
		case <-ctx.Done():
			// Abort: cancel every in-flight attempt. Their goroutines drain
			// into the buffered channel and exit; nothing is returned to close
			// because a cancelled dial yields an error, not an *Established.
			for i, c := range cancels {
				if c != nil && !done[i] {
					c()
				}
			}
			return nil, ctx.Err()

		case <-timer.C:
			if next < len(cands) {
				launch(next)
				next++
				if next < len(cands) {
					timer.Reset(delay)
				}
			}

		case res := <-results:
			done[res.i] = true
			inflight--
			label := cands[res.i].Label

			if res.err == nil {
				// §6: first success wins. Cancel every other in-flight attempt
				// and stop launching. Any straggler that completes despite its
				// cancellation is closed by drainClose, never leaked.
				if tl != nil {
					tl.Add("success", label, "")
					tl.Add("winner", label, "")
				}
				for i, c := range cancels {
					if c != nil && !done[i] {
						c()
						if tl != nil {
							tl.Add("cancel", cands[i].Label, "")
						}
					}
				}
				stopTimer()
				if inflight > 0 {
					go drainClose(results, inflight)
				}
				return withCancelOnClose(res.est, cancels[res.i]), nil
			}

			failErrs[res.i] = res.err
			if tl != nil {
				tl.Add("fail", label, res.err.Error())
			}
			switch {
			case res.i == lastLaunched && next < len(cands):
				// §6: the most-recent attempt failed before its stagger slot
				// elapsed, so promote the next candidate now — a dead stagger
				// slot would waste time no attempt is using.
				stopTimer()
				launch(next)
				next++
				if next < len(cands) {
					timer.Reset(delay)
				}
			case inflight == 0 && next >= len(cands):
				// Everything launched and every attempt failed.
				return nil, joinFailures(cands, failErrs)
			}
		}
	}
}

// attemptResult carries one finished dial back to the launcher loop.
type attemptResult struct {
	i   int
	est *Established
	err error
}

// drainClose reads n straggler results and closes any that succeeded, so a
// connection that won its dial after losing the race is not leaked (§6).
func drainClose(results <-chan attemptResult, n int) {
	for k := 0; k < n; k++ {
		r := <-results
		if r.err == nil && r.est != nil && r.est.Close != nil {
			_ = r.est.Close()
		}
	}
}

// withCancelOnClose defers the winner's dial-context cancellation until the
// caller closes the connection, so the context is released (not leaked) without
// tearing down the freshly established transport mid-use.
func withCancelOnClose(est *Established, cancel context.CancelFunc) *Established {
	if est == nil {
		return est
	}
	orig := est.Close
	est.Close = func() error {
		if cancel != nil {
			cancel()
		}
		if orig != nil {
			return orig()
		}
		return nil
	}
	return est
}

// joinFailures builds an error that names every failed candidate by Label and
// wraps each underlying cause (so errors.Is finds them).
func joinFailures(cands []Candidate, errs []error) error {
	var joined []error
	for i, e := range errs {
		if e != nil {
			joined = append(joined, fmt.Errorf("%s: %w", cands[i].Label, e))
		}
	}
	return fmt.Errorf("hev3: all %d candidates failed: %w", len(joined), errors.Join(joined...))
}
