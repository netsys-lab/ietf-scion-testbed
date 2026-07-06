// Package ratelimit is a small sliding-window rate limiter with an injectable
// clock, used to throttle POST /api/join/claim per client (booth-code brute
// force defense). Keys come from ClientKey: full IPv4 address, or the IPv6
// /64 network so a client rotating within its block can't evade the limit.
package ratelimit

import (
	"net"
	"sync"
	"time"
)

type Limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	hits   map[string][]time.Time
}

func New(max int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{max: max, window: window, now: now, hits: make(map[string][]time.Time)}
}

// Allow records and permits an event for key when fewer than max events fall
// within the trailing window; otherwise it denies without recording.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

// ClientKey turns an http.Request RemoteAddr ("host:port") into a limiter key.
func ClientKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String()
}
