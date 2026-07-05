package shape

import "testing"

func f(v float64) *float64 { return &v }

func TestValidateCaps(t *testing.T) {
	cases := []struct {
		name string
		p    Params
		ok   bool
	}{
		{"empty", Params{}, true},
		{"good", Params{DelayMs: f(50), JitterMs: f(5), LossPct: f(1), RateMbit: f(50)}, true},
		{"delay too big", Params{DelayMs: f(2001)}, false},
		{"negative delay", Params{DelayMs: f(-1)}, false},
		{"jitter without delay", Params{JitterMs: f(5)}, false},
		{"jitter too big", Params{DelayMs: f(10), JitterMs: f(1001)}, false},
		{"loss over 100", Params{LossPct: f(101)}, false},
		{"rate too small", Params{RateMbit: f(0.05)}, false},
		{"rate too big", Params{RateMbit: f(10001)}, false},
		{"delay at cap", Params{DelayMs: f(2000)}, true},
		{"jitter at cap", Params{DelayMs: f(1), JitterMs: f(1000)}, true},
		{"loss at cap", Params{LossPct: f(100)}, true},
		{"rate at cap", Params{RateMbit: f(10000)}, true},
		{"rate at floor", Params{RateMbit: f(0.1)}, true},
		{"zero jitter without delay", Params{JitterMs: f(0)}, true},
	}
	for _, c := range cases {
		if err := Validate(c.p); (err == nil) != c.ok {
			t.Errorf("%s: err=%v want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestMerge(t *testing.T) {
	cur := Params{DelayMs: f(10), LossPct: f(2)}
	got := Merge(cur, Params{DelayMs: f(50), RateMbit: f(20)})
	if *got.DelayMs != 50 || *got.LossPct != 2 || *got.RateMbit != 20 || got.JitterMs != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestEmpty(t *testing.T) {
	if !(Params{}).Empty() {
		t.Fatal("zero Params must be Empty")
	}
	if (Params{DelayMs: f(1)}).Empty() {
		t.Fatal("delay set must not be Empty")
	}
}
