package baseline

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	// rate_mbit kept <= 1000: shape.Validate's cap is raised to 10000 in Task 4,
	// so real core-tier baselines (10000) can't be exercised here yet.
	p := write(t, t.TempDir(), "linkd-baseline.json",
		`{"18982": {"delay_ms": 3.0, "rate_mbit": 1000}, "6300": {"delay_ms": 9.9, "rate_mbit": 500}}`)
	m, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 {
		t.Fatalf("want 2 entries, got %d", len(m))
	}
	e := m["18982"]
	if e.DelayMs == nil || *e.DelayMs != 3.0 {
		t.Fatalf("delay_ms = %v, want 3.0", e.DelayMs)
	}
	if e.RateMbit == nil || *e.RateMbit != 1000 {
		t.Fatalf("rate_mbit = %v, want 1000", e.RateMbit)
	}
	if e.JitterMs != nil || e.LossPct != nil {
		t.Fatal("jitter/loss must stay nil in baselines")
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	p := write(t, t.TempDir(), "bad.json", `{"1": {"delay_ms": -5}}`)
	if _, err := Load(p); err == nil {
		t.Fatal("want validation error for negative delay")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("want error for missing file")
	}
}

func TestResolveOne(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.json", "{}")
	got, err := ResolveOne(filepath.Join(dir, "*.json"))
	if err != nil || filepath.Base(got) != "a.json" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ResolveOne(filepath.Join(dir, "*.nope")); err == nil {
		t.Fatal("want error for zero matches")
	}
	write(t, dir, "b.json", "{}")
	if _, err := ResolveOne(filepath.Join(dir, "*.json")); err == nil {
		t.Fatal("want error for two matches")
	}
}
