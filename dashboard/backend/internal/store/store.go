// Package store provides an in-memory, ring-buffer-backed time series store
// for scraped Prometheus samples. Each key holds a fixed-capacity ring of
// (timestamp, value) samples. Writers (scrapers) and readers (the API) may
// use the Store concurrently.
package store

import (
	"sort"
	"strings"
	"sync"
)

// Sample is a single (timestamp, value) observation. T is a unix
// millisecond timestamp. The json tags are the history endpoint's wire
// protocol ([{"t":..,"v":..}]); do not change them without updating the API.
type Sample struct {
	T int64   `json:"t"`
	V float64 `json:"v"`
}

// ring is a fixed-capacity circular buffer of samples for one key.
type ring struct {
	buf   []Sample
	cap   int
	next  int // index the next Put will write to
	count int // number of valid samples currently stored (<= cap)
}

func newRing(capacity int) *ring {
	if capacity <= 0 {
		capacity = 1
	}
	return &ring{buf: make([]Sample, capacity), cap: capacity}
}

func (r *ring) put(t int64, v float64) {
	r.buf[r.next] = Sample{T: t, V: v}
	r.next = (r.next + 1) % r.cap
	if r.count < r.cap {
		r.count++
	}
}

func (r *ring) last() Sample {
	idx := (r.next - 1 + r.cap) % r.cap
	return r.buf[idx]
}

// lastN returns the most recent n samples (or fewer, if the ring holds
// fewer than n) in chronological (oldest-first) order.
func (r *ring) lastN(n int) []Sample {
	if n > r.count {
		n = r.count
	}
	if n <= 0 {
		return nil
	}
	start := ((r.next-n)%r.cap + r.cap) % r.cap
	out := make([]Sample, n)
	for i := 0; i < n; i++ {
		out[i] = r.buf[(start+i)%r.cap]
	}
	return out
}

// Store holds one ring per key, all sharing a single capacity, guarded by
// one RWMutex. Safe for concurrent use by multiple scraper goroutines
// (writers) and the API (reader).
type Store struct {
	mu   sync.RWMutex
	cap  int
	data map[string]*ring
}

// New creates a Store where every key's ring holds up to capacity samples.
func New(capacity int) *Store {
	if capacity <= 0 {
		capacity = 1
	}
	return &Store{cap: capacity, data: make(map[string]*ring)}
}

// Put appends a sample for key, overwriting the oldest sample once the
// key's ring is at capacity.
func (s *Store) Put(key string, t int64, v float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.data[key]
	if !ok {
		r = newRing(s.cap)
		s.data[key] = r
	}
	r.put(t, v)
}

// Last returns the most recently written sample for key, if any.
func (s *Store) Last(key string) (Sample, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.data[key]
	if !ok || r.count == 0 {
		return Sample{}, false
	}
	return r.last(), true
}

// Series returns all stored samples for key with T >= sinceMs, in
// chronological order.
func (s *Store) Series(key string, sinceMs int64) []Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.data[key]
	if !ok {
		return nil
	}
	all := r.lastN(r.count)
	out := make([]Sample, 0, len(all))
	for _, sm := range all {
		if sm.T >= sinceMs {
			out = append(out, sm)
		}
	}
	return out
}

// Rate computes the per-second rate of increase of a counter over the last
// window samples for key. It walks consecutive sample pairs, summing
// positive deltas and their time spans while skipping any decrease (a
// counter reset starts a new segment). The result is never negative and is
// 0 if fewer than 2 samples are available.
func (s *Store) Rate(key string, window int) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.data[key]
	if !ok {
		return 0
	}
	n := window
	if n > r.count {
		n = r.count
	}
	if n < 2 {
		return 0
	}
	samples := r.lastN(n)

	var sumV, sumSeconds float64
	for i := 1; i < len(samples); i++ {
		dv := samples[i].V - samples[i-1].V
		dtMs := samples[i].T - samples[i-1].T
		if dv > 0 && dtMs > 0 {
			sumV += dv
			sumSeconds += float64(dtMs) / 1000.0
		}
	}
	if sumSeconds <= 0 {
		return 0
	}
	rate := sumV / sumSeconds
	if rate < 0 {
		rate = 0
	}
	return rate
}

// Keys returns all keys currently in the store that start with prefix,
// sorted lexicographically.
func (s *Store) Keys(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.data))
	for k := range s.data {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
