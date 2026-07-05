// Package shape applies netem link shaping and validates its parameters.
package shape

import "fmt"

type Params struct {
	DelayMs  *float64 `json:"delay_ms,omitempty"`
	JitterMs *float64 `json:"jitter_ms,omitempty"`
	LossPct  *float64 `json:"loss_pct,omitempty"`
	RateMbit *float64 `json:"rate_mbit,omitempty"`
}

func (p Params) Empty() bool {
	zero := func(v *float64) bool { return v == nil || *v == 0 }
	return zero(p.DelayMs) && zero(p.JitterMs) && zero(p.LossPct) && p.RateMbit == nil
}

func Validate(p Params) error {
	check := func(name string, v *float64, lo, hi float64) error {
		if v != nil && (*v < lo || *v > hi) {
			return fmt.Errorf("%s %.2f out of range [%g, %g]", name, *v, lo, hi)
		}
		return nil
	}
	if err := check("delay_ms", p.DelayMs, 0, 2000); err != nil {
		return err
	}
	if err := check("jitter_ms", p.JitterMs, 0, 1000); err != nil {
		return err
	}
	if err := check("loss_pct", p.LossPct, 0, 100); err != nil {
		return err
	}
	if err := check("rate_mbit", p.RateMbit, 0.1, 10000); err != nil {
		return err
	}
	if p.JitterMs != nil && *p.JitterMs > 0 && (p.DelayMs == nil || *p.DelayMs == 0) {
		return fmt.Errorf("jitter_ms requires delay_ms > 0")
	}
	return nil
}

func Merge(cur, upd Params) Params {
	out := cur
	if upd.DelayMs != nil {
		out.DelayMs = upd.DelayMs
	}
	if upd.JitterMs != nil {
		out.JitterMs = upd.JitterMs
	}
	if upd.LossPct != nil {
		out.LossPct = upd.LossPct
	}
	if upd.RateMbit != nil {
		out.RateMbit = upd.RateMbit
	}
	return out
}

// Shaper reads and writes a single netem root qdisc per device.
// The kernel is the only source of truth; Get always reflects it.
type Shaper interface {
	Get(dev string) (Params, error)
	Apply(dev string, p Params) error
	Clear(dev string) error
}
