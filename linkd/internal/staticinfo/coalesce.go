package staticinfo

import (
	"sync"
	"time"
)

// Coalesce wraps f so that rapid, repeated calls to the returned function
// collapse into at most two invocations of f per window of duration d: the
// first call in a quiescent period runs f immediately (leading edge), and
// any further calls that arrive before the window elapses are coalesced
// into a single trailing call once it does. A call that arrives after the
// window has elapsed with nothing pending starts a fresh window and again
// runs f immediately.
//
// This is what turns a burst of dashboard clicks (each triggering a
// staticinfo OnChange) into at most one immediate CS reload plus one
// trailing reload, instead of one SIGHUP per click.
//
// Coalesce is timer-based: it holds no long-lived goroutine. f may run
// concurrently with itself in the leading/trailing race at a window
// boundary; callers whose f is not internally serialized should guard it
// themselves (staticinfo.Writer.Write already does, via its own mutex).
func Coalesce(d time.Duration, f func()) func() {
	c := &coalescer{d: d, f: f}
	return c.call
}

type coalescer struct {
	mu    sync.Mutex
	d     time.Duration
	f     func()
	timer *time.Timer
	dirty bool
}

func (c *coalescer) call() {
	c.mu.Lock()
	if c.timer == nil {
		// Quiescent: fire the leading edge now and open a window during
		// which further calls only mark dirty.
		c.timer = time.AfterFunc(c.d, c.fire)
		c.mu.Unlock()
		c.f()
		return
	}
	c.dirty = true
	c.mu.Unlock()
}

func (c *coalescer) fire() {
	c.mu.Lock()
	dirty := c.dirty
	c.dirty = false
	c.timer = nil
	c.mu.Unlock()
	if dirty {
		c.f()
	}
}
