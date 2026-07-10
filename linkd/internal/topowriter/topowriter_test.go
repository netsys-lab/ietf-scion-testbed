package topowriter

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/shape"
)

const base = `{
 "border_routers": {
  "br1-150-1": {
   "interfaces": {
    "18982": {"idint": {"speed": 100000000}, "underlay": {"local": "[fd00::1]:50000"}, "isd_as": "1-151", "link_to": "child"},
    "6300": {"idint": {"speed": 50000000}, "underlay": {"local": "[fd00::2]:50000"}, "isd_as": "1-152", "link_to": "core"}
   },
   "idint": {"id": 1, "internal_speed": 1000000000}
  }
 },
 "isd_as": "1-150",
 "mtu": 1452
}`

func f64(v float64) *float64 { return &v }

func newWriter(t *testing.T) (*Writer, *[]string) {
	t.Helper()
	dir := t.TempDir()
	bp := filepath.Join(dir, "topology.base.json")
	if err := os.WriteFile(bp, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	var signaled []string
	w := &Writer{
		BasePath: bp,
		OutPath:  filepath.Join(dir, "topology.json"),
		Unit:     "br.service",
		Signal:   func(u string) error { signaled = append(signaled, u); return nil },
	}
	return w, &signaled
}

func speed(t *testing.T, w *Writer, ifid string) any {
	t.Helper()
	raw, err := os.ReadFile(w.OutPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	br := doc["border_routers"].(map[string]any)["br1-150-1"].(map[string]any)
	return br["interfaces"].(map[string]any)[ifid].(map[string]any)["idint"].(map[string]any)["speed"]
}

func TestShapedRateOverridesSpeed(t *testing.T) {
	w, signaled := newWriter(t)
	if err := w.Write(map[string]shape.Params{"18982": {RateMbit: f64(25)}}); err != nil {
		t.Fatal(err)
	}
	if got := speed(t, w, "18982"); got != float64(25000000) { // 25 Mbit -> 25e6 bits/s
		t.Fatalf("idint.speed = %v, want 25000000", got)
	}
	if got := speed(t, w, "6300"); got != float64(50000000) { // unshaped keeps nominal
		t.Fatalf("unshaped idint.speed = %v, want base 50000000", got)
	}
	raw, _ := os.ReadFile(w.OutPath)
	var doc map[string]any
	json.Unmarshal(raw, &doc)
	br := doc["border_routers"].(map[string]any)["br1-150-1"].(map[string]any)
	if got := br["idint"].(map[string]any)["internal_speed"]; got != float64(1000000000) {
		t.Fatalf("internal_speed = %v, want 1000000000 (untouched)", got)
	}
	if doc["mtu"] != float64(1452) || doc["isd_as"] != "1-150" {
		t.Fatal("static topology fields modified")
	}
	if len(*signaled) != 1 || (*signaled)[0] != "br.service" {
		t.Fatalf("signaled = %v", *signaled)
	}
	if m, r := w.Status(); !m || !r {
		t.Fatal("Status should be ok/ok")
	}
}

func TestNilRateKeepsBaseSpeed(t *testing.T) {
	w, _ := newWriter(t)
	if err := w.Write(map[string]shape.Params{"18982": {DelayMs: f64(30)}}); err != nil {
		t.Fatal(err)
	}
	if got := speed(t, w, "18982"); got != float64(100000000) {
		t.Fatalf("idint.speed = %v, want base 100000000 (latency-only shape must not change speed)", got)
	}
}

func TestNoOpWriteSkipsSignal(t *testing.T) {
	w, signaled := newWriter(t)
	live := map[string]shape.Params{"18982": {RateMbit: f64(25)}}
	if err := w.Write(live); err != nil {
		t.Fatal(err)
	}
	fi1, _ := os.Stat(w.OutPath)
	if err := w.Write(live); err != nil {
		t.Fatal(err)
	}
	if len(*signaled) != 1 {
		t.Fatalf("second identical write signaled again: %v", *signaled)
	}
	fi2, _ := os.Stat(w.OutPath)
	if !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Fatal("output rewritten on no-op write")
	}
}

func TestSignalFailureReported(t *testing.T) {
	w, _ := newWriter(t)
	w.Signal = func(string) error { return errors.New("boom") }
	if err := w.Write(map[string]shape.Params{"18982": {RateMbit: f64(25)}}); err == nil {
		t.Fatal("want error")
	}
	if m, r := w.Status(); !m || r {
		t.Fatalf("Status = %v,%v; want metadata ok, reload failed", m, r)
	}
}

func TestMissingBaseReported(t *testing.T) {
	w, _ := newWriter(t)
	w.BasePath += ".nope"
	if err := w.Write(nil); err == nil {
		t.Fatal("want error")
	}
	if m, r := w.Status(); m || r {
		t.Fatalf("Status = %v,%v; want false,false", m, r)
	}
}
