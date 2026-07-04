package scrape

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

func mustReadFixture(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/br.metrics")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func closeEnough(a, b float64) bool {
	const eps = 1e-9
	if a > b {
		return a-b < eps
	}
	return b-a < eps
}

func TestScrapeOnceSuccess(t *testing.T) {
	fixture := mustReadFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture)
	}))
	defer srv.Close()

	st := store.New(8)
	targets := []Target{{AS: 155, Service: "br", URL: srv.URL + "/metrics"}}
	sc := New(st, targets, time.Second, srv.Client())

	sc.ScrapeOnce(context.Background())

	checks := []struct {
		key  string
		want float64
	}{
		{"155/br/rtt/6049", 2.1},
		{"155/br/output_bytes/6049", 3000},
		{"155/br/input_bytes/6049", 500},
		{"155/br/up/6049", 1},
		{"155/br/dropped_pkts/6049", 7},
		{"155/br/_up/", 1},
	}
	for _, c := range checks {
		s, ok := st.Last(c.key)
		if !ok {
			t.Fatalf("key %s: not found", c.key)
		}
		if !closeEnough(s.V, c.want) {
			t.Fatalf("key %s: got %v want %v", c.key, s.V, c.want)
		}
	}
}

func TestScrapeOnceHTTPErrorLeavesStale(t *testing.T) {
	fixture := mustReadFixture(t)
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write(fixture)
	}))
	defer srv.Close()

	st := store.New(8)
	targets := []Target{{AS: 155, Service: "br", URL: srv.URL + "/metrics"}}
	sc := New(st, targets, time.Second, srv.Client())

	sc.ScrapeOnce(context.Background())
	if s, ok := st.Last("155/br/_up/"); !ok || s.V != 1 {
		t.Fatalf("expected healthy after first scrape, got %+v ok=%v", s, ok)
	}

	fail = true
	sc.ScrapeOnce(context.Background())

	if s, ok := st.Last("155/br/_up/"); !ok || s.V != 0 {
		t.Fatalf("expected _up=0 after failed scrape, got %+v ok=%v", s, ok)
	}
	// Stale series from the first (successful) scrape must still be readable.
	if s, ok := st.Last("155/br/output_bytes/6049"); !ok || !closeEnough(s.V, 3000) {
		t.Fatalf("expected stale output_bytes to remain, got %+v ok=%v", s, ok)
	}
	if s, ok := st.Last("155/br/rtt/6049"); !ok || !closeEnough(s.V, 2.1) {
		t.Fatalf("expected stale rtt to remain, got %+v ok=%v", s, ok)
	}
}

func TestScrapeOnceCSBeaconFamilies(t *testing.T) {
	body := `
# TYPE control_beaconing_received_beacons_total counter
control_beaconing_received_beacons_total{ingress_interface="6049",result="success"} 3
control_beaconing_received_beacons_total{ingress_interface="6049",result="filtered"} 1
# TYPE control_beaconing_propagated_beacons_total counter
control_beaconing_propagated_beacons_total{egress_interface="6050"} 9
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	st := store.New(8)
	targets := []Target{{AS: 155, Service: "cs", URL: srv.URL + "/metrics"}}
	sc := New(st, targets, time.Second, srv.Client())

	sc.ScrapeOnce(context.Background())

	if s, ok := st.Last("155/cs/beacons_recv/6049"); !ok || !closeEnough(s.V, 4) {
		t.Fatalf("beacons_recv: got %+v ok=%v", s, ok)
	}
	if s, ok := st.Last("155/cs/beacons_prop/6050"); !ok || !closeEnough(s.V, 9) {
		t.Fatalf("beacons_prop: got %+v ok=%v", s, ok)
	}
}

func TestRunScrapesImmediatelyThenStopsOnCancel(t *testing.T) {
	fixture := mustReadFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture)
	}))
	defer srv.Close()

	st := store.New(8)
	targets := []Target{{AS: 155, Service: "br", URL: srv.URL + "/metrics"}}
	// Interval much longer than the test timeout, so only the immediate
	// pre-ticker scrape can have populated the store when we check.
	sc := New(st, targets, time.Hour, srv.Client())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sc.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := st.Last("155/br/_up/"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not perform an immediate scrape in time")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancellation")
	}
}

func TestTargets(t *testing.T) {
	g := topo.Graph{ASes: []topo.AS{
		{Num: 155, MgmtIP: "10.20.3.155"},
		{Num: 160, MgmtIP: "10.20.3.160"},
	}}
	got := Targets(g)
	if len(got) != 6 {
		t.Fatalf("want 6 targets (3 per AS), got %d: %+v", len(got), got)
	}
	want := map[string]bool{
		"155/br": true, "155/cs": true, "155/sd": true,
		"160/br": true, "160/cs": true, "160/sd": true,
	}
	urls := map[string]string{}
	for _, tg := range got {
		key := ""
		switch tg.Service {
		case "br", "cs", "sd":
		default:
			t.Fatalf("unexpected service %q", tg.Service)
		}
		key = strconv.Itoa(tg.AS) + "/" + tg.Service
		if !want[key] {
			t.Fatalf("unexpected target %+v", tg)
		}
		delete(want, key)
		urls[key] = tg.URL
	}
	if len(want) != 0 {
		t.Fatalf("missing targets: %+v", want)
	}
	if urls["155/br"] != "http://10.20.3.155:30442/metrics" {
		t.Fatalf("br URL wrong: %s", urls["155/br"])
	}
	if urls["155/cs"] != "http://10.20.3.155:30452/metrics" {
		t.Fatalf("cs URL wrong: %s", urls["155/cs"])
	}
	if urls["155/sd"] != "http://10.20.3.155:30455/metrics" {
		t.Fatalf("sd URL wrong: %s", urls["155/sd"])
	}
}
