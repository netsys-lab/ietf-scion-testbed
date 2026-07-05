package staticinfo

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/shape"
)

const base = `{
 "Bandwidth": {"18982": {"Inter": 10000000, "Intra": {"20879": 100000000}}},
 "Geo": {"18982": {"Address": "Vienna", "Latitude": 48.2082, "Longitude": 16.3738}},
 "Hops": {"18982": {"Intra": {"20879": 0}}},
 "Latency": {"18982": {"Inter": "3000us", "Intra": {"20879": "0us"}}},
 "LinkType": {"18982": "direct"},
 "Note": "test"
}`

// durationRE mirrors the deployed CS fork's integer-only duration grammar
// (scion fork pkg/private/util/duration.go): any value we write must match.
var durationRE = regexp.MustCompile(`^-?[0-9]+(y|w|d|h|m|s|ms|us|µs|ns)$`)

func f64(v float64) *float64 { return &v }

func newWriter(t *testing.T) (*Writer, string, *[]string) {
	t.Helper()
	dir := t.TempDir()
	bp := filepath.Join(dir, "staticInfoConfig.base.json")
	if err := os.WriteFile(bp, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	var signaled []string
	w := &Writer{
		BasePath: bp,
		OutPath:  filepath.Join(dir, "staticInfoConfig.json"),
		Unit:     "cs.service",
		Signal:   func(u string) error { signaled = append(signaled, u); return nil },
	}
	return w, dir, &signaled
}

func readOut(t *testing.T, w *Writer) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(w.OutPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func inter(t *testing.T, doc map[string]any, section, ifid string) any {
	t.Helper()
	return doc[section].(map[string]any)[ifid].(map[string]any)["Inter"]
}

func TestWriteOverridesInter(t *testing.T) {
	w, _, signaled := newWriter(t)
	err := w.Write(map[string]shape.Params{
		"18982": {DelayMs: f64(60.5), RateMbit: f64(500)},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc := readOut(t, w)
	got, _ := inter(t, doc, "Latency", "18982").(string)
	if got != "60500us" {
		t.Fatalf("Latency Inter = %v, want 60500us", got)
	}
	if !durationRE.MatchString(got) {
		t.Fatalf("Latency Inter = %q does not match integer duration grammar %s", got, durationRE)
	}
	if got := inter(t, doc, "Bandwidth", "18982"); got != float64(500000) {
		t.Fatalf("Bandwidth Inter = %v, want 500000", got)
	}
	// static sections pass through untouched
	if doc["Note"] != "test" || doc["LinkType"].(map[string]any)["18982"] != "direct" {
		t.Fatal("static sections modified")
	}
	if got := doc["Latency"].(map[string]any)["18982"].(map[string]any)["Intra"].(map[string]any)["20879"]; got != "0us" {
		t.Fatal("Intra modified")
	}
	if len(*signaled) != 1 || (*signaled)[0] != "cs.service" {
		t.Fatalf("signaled = %v", *signaled)
	}
	if m, r := w.Status(); !m || !r {
		t.Fatal("Status should be ok/ok")
	}
}

func TestWriteNilFieldsKeepBase(t *testing.T) {
	w, _, _ := newWriter(t)
	if err := w.Write(map[string]shape.Params{"18982": {}}); err != nil {
		t.Fatal(err)
	}
	doc := readOut(t, w)
	if got := inter(t, doc, "Latency", "18982"); got != "3000us" {
		t.Fatalf("Latency Inter = %v, want base 3000us", got)
	}
	if got := inter(t, doc, "Bandwidth", "18982"); got != float64(10000000) {
		t.Fatalf("Bandwidth Inter = %v, want base 10000000", got)
	}
}

func TestWriteUnknownIfidCreatesEntry(t *testing.T) {
	w, _, _ := newWriter(t)
	if err := w.Write(map[string]shape.Params{"999": {DelayMs: f64(5)}}); err != nil {
		t.Fatal(err)
	}
	doc := readOut(t, w)
	if got := inter(t, doc, "Latency", "999"); got != "5000us" {
		t.Fatalf("Latency Inter = %v, want 5000us", got)
	}
}

func TestNoSignalWithoutUnit(t *testing.T) {
	w, _, signaled := newWriter(t)
	w.Unit = ""
	if err := w.Write(nil); err != nil {
		t.Fatal(err)
	}
	if len(*signaled) != 0 {
		t.Fatal("must not signal with empty unit")
	}
}

func TestSignalFailureReported(t *testing.T) {
	w, _, _ := newWriter(t)
	w.Signal = func(string) error { return errors.New("boom") }
	if err := w.Write(nil); err == nil {
		t.Fatal("want error")
	}
	if m, r := w.Status(); !m || r {
		t.Fatalf("Status = %v,%v; want metadata ok, reload failed", m, r)
	}
}

func TestMissingBaseReported(t *testing.T) {
	w, _, _ := newWriter(t)
	w.BasePath = w.BasePath + ".nope"
	if err := w.Write(nil); err == nil {
		t.Fatal("want error")
	}
	if m, _ := w.Status(); m {
		t.Fatal("metadataOK must be false")
	}
}
