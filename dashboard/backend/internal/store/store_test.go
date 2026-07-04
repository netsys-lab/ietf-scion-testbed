package store

import "testing"

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
