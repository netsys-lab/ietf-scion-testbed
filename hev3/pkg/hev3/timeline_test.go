package hev3

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

// concurrent Add from 10 goroutines must not race and must preserve every
// event; At must be non-decreasing when events are read back in Events()
// call order relative to when Add returned (see below: we assert the
// weaker, race-safe invariant that At is monotonic non-decreasing once
// sorted, and that no event's At is negative).
func TestTimeline_ConcurrentAdd(t *testing.T) {
	var tl Timeline
	const goroutines = 10
	const perGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				tl.Add("candidate", fmt.Sprintf("g%d-i%d", g, i), "detail")
			}
		}(g)
	}
	wg.Wait()

	events := tl.Events()
	if len(events) != goroutines*perGoroutine {
		t.Fatalf("got %d events, want %d", len(events), goroutines*perGoroutine)
	}

	seen := make(map[string]bool, len(events))
	for _, e := range events {
		if e.At < 0 {
			t.Fatalf("event %+v has negative At", e)
		}
		if e.Kind != "candidate" {
			t.Fatalf("event %+v has unexpected Kind", e)
		}
		if seen[e.Label] {
			t.Fatalf("duplicate label %q in events", e.Label)
		}
		seen[e.Label] = true
	}
	for g := 0; g < goroutines; g++ {
		for i := 0; i < perGoroutine; i++ {
			label := fmt.Sprintf("g%d-i%d", g, i)
			if !seen[label] {
				t.Fatalf("missing event for %q", label)
			}
		}
	}

	// Events() returns them in insertion (call-completion) order; the
	// mutex serializes Add, so At must be non-decreasing as recorded.
	if !sort.SliceIsSorted(events, func(i, j int) bool { return events[i].At < events[j].At }) {
		t.Fatalf("events not monotonic non-decreasing by At: %+v", events)
	}
}

func TestTimeline_KindsAndDetailPreserved(t *testing.T) {
	var tl Timeline
	tl.Add("query", "host.scion.", "SVCB")
	tl.Add("answer", "host.scion.", "3 records")
	tl.Add("winner", "scion:1-150#p1", "")

	events := tl.Events()
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	want := []Event{
		{Kind: "query", Label: "host.scion.", Detail: "SVCB"},
		{Kind: "answer", Label: "host.scion.", Detail: "3 records"},
		{Kind: "winner", Label: "scion:1-150#p1", Detail: ""},
	}
	for i, w := range want {
		if events[i].Kind != w.Kind || events[i].Label != w.Label || events[i].Detail != w.Detail {
			t.Fatalf("event %d = %+v, want %+v", i, events[i], w)
		}
	}
}

// Events() must return a defensive copy: mutating it must not corrupt the
// Timeline's internal state.
func TestTimeline_EventsReturnsCopy(t *testing.T) {
	var tl Timeline
	tl.Add("query", "a", "")
	tl.Add("query", "b", "")

	events := tl.Events()
	events[0].Label = "corrupted"

	fresh := tl.Events()
	if fresh[0].Label != "a" {
		t.Fatalf("internal state mutated via Events() slice: got %q, want %q", fresh[0].Label, "a")
	}
}
