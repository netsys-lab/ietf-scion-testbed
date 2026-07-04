package store

import (
	"fmt"
	"sync"
	"testing"
)

func TestPutLastAndWrap(t *testing.T) {
	s := New(4)
	for i := 0; i < 6; i++ {
		s.Put("k", int64(i*1000), float64(i))
	}
	last, ok := s.Last("k")
	if !ok || last.V != 5 {
		t.Fatalf("last %+v", last)
	}
	if got := s.Series("k", 0); len(got) != 4 || got[0].V != 2 {
		t.Fatalf("series %+v", got)
	}
}

func TestRateCounter(t *testing.T) {
	s := New(16)
	for i := 0; i < 5; i++ {
		s.Put("c", int64(i*1000), float64(i)*1e6)
	}
	r := s.Rate("c", 5)
	if r < 0.99e6 || r > 1.01e6 {
		t.Fatalf("rate %f", r)
	}
}

func TestRateReset(t *testing.T) {
	s := New(16)
	vals := []float64{100, 200, 50, 150}
	for i, v := range vals {
		s.Put("c", int64(i*1000), v)
	}
	r := s.Rate("c", 4)
	if r <= 0 || r > 101 {
		t.Fatalf("reset-aware rate %f", r)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := New(64)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				s.Put(fmt.Sprintf("k%d", w%2), int64(i*10), float64(i))
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				s.Last("k0")
				s.Series("k1", 0)
				s.Rate("k0", 16)
				s.Keys("k")
			}
		}()
	}
	wg.Wait()
}
