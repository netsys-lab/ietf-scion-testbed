package hev3

import (
	"sync"
	"time"
)

// Event is one recorded step of a resolve/race attempt. Kind is one of:
// query, answer, candidate, attempt, success, fail, cancel, winner.
type Event struct {
	At     time.Duration
	Kind   string
	Label  string
	Detail string
}

// Timeline records timestamped events relative to its first use. The zero
// value is ready to use — start time is set lazily on the first Add, so
// callers never need a constructor.
type Timeline struct {
	mu     sync.Mutex
	once   sync.Once
	start  time.Time
	events []Event
}

// Add appends an event timestamped relative to the Timeline's start
// (the moment of the first Add call). Safe for concurrent use.
func (t *Timeline) Add(kind, label, detail string) {
	t.once.Do(func() { t.start = time.Now() })
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, Event{
		At:     time.Since(t.start),
		Kind:   kind,
		Label:  label,
		Detail: detail,
	})
}

// Events returns a snapshot copy of all recorded events, in Add order.
func (t *Timeline) Events() []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Event, len(t.events))
	copy(out, t.events)
	return out
}
