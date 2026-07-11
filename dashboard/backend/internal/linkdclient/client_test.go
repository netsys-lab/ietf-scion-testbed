package linkdclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// recorded captures one request a fakeLinkd handled.
type recorded struct {
	method string
	path   string
	body   string
}

// fakeLinkd is a minimal stand-in for scion-linkd's REST API: it serves
// GET /api/v1/links from a canned body, and PUT/DELETE on
// /api/v1/links/{ifid} while recording every request it sees. status, when
// non-zero, forces every subsequent request to fail with that code.
type fakeLinkd struct {
	mu      sync.Mutex
	reqs    []recorded
	getJSON string
	status  int
}

func newFakeLinkd(getJSON string) (*httptest.Server, *fakeLinkd) {
	f := &fakeLinkd{getJSON: getJSON}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/links", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if code := f.statusOverride(); code != 0 {
			w.WriteHeader(code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(f.getJSON))
	})
	mux.HandleFunc("PUT /api/v1/links/{ifid}", func(w http.ResponseWriter, r *http.Request) {
		body := f.record(r)
		if code := f.statusOverride(); code != 0 {
			w.WriteHeader(code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	})
	mux.HandleFunc("DELETE /api/v1/links/{ifid}", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if code := f.statusOverride(); code != 0 {
			w.WriteHeader(code)
			return
		}
		w.Write([]byte(`{"status":"cleared"}`))
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		f.record(r)
		if code := f.statusOverride(); code != 0 {
			w.WriteHeader(code)
			return
		}
		w.Write([]byte(`{"status":"ok"}`))
	})
	return httptest.NewServer(mux), f
}

func (f *fakeLinkd) record(r *http.Request) string {
	buf, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, recorded{method: r.Method, path: r.URL.Path, body: string(buf)})
	return string(buf)
}

func (f *fakeLinkd) setStatus(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = code
}

func (f *fakeLinkd) statusOverride() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeLinkd) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recorded, len(f.reqs))
	copy(out, f.reqs)
	return out
}

// testGraph builds a Graph with the single 150-151 link, with AS150's and
// AS151's MgmtIP pointing at aURL and bURL respectively (as returned by
// httptest.Server.URL, e.g. "http://127.0.0.1:54321").
func testGraph(aURL, bURL string) topo.Graph {
	return topo.Graph{
		ASes: []topo.AS{
			{IA: "1-150", Num: 150, MgmtIP: strings.TrimPrefix(aURL, "http://")},
			{IA: "1-151", Num: 151, MgmtIP: strings.TrimPrefix(bURL, "http://")},
		},
		Links: []topo.Link{{
			ID:     "150-151",
			Type:   "core",
			Subnet: "link 1",
			A:      topo.Endpoint{IA: "1-150", AS: 150, IfID: "6049", IP: "fd00:fade:1::150", LinkTo: "child"},
			B:      topo.Endpoint{IA: "1-151", AS: 151, IfID: "150", IP: "fd00:fade:1::151", LinkTo: "parent"},
		}},
	}
}

func testClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Second}
}

func TestPollReturnsASideShaping(t *testing.T) {
	aJSON := `[{"ifid":"6049","neighbor_isd_as":"1-151","link_to":"child","device":"sci9","shaping":{"delay_ms":25},"shaped":true}]`
	bJSON := `[{"ifid":"150","neighbor_isd_as":"1-150","link_to":"parent","device":"sci9","shaping":{"delay_ms":999}}]`
	aSrv, _ := newFakeLinkd(aJSON)
	defer aSrv.Close()
	bSrv, _ := newFakeLinkd(bJSON)
	defer bSrv.Close()

	g := testGraph(aSrv.URL, bSrv.URL)
	c := New(g, testClient())

	got, _ := c.Poll(context.Background())
	sh, ok := got["150-151"]
	if !ok || sh == nil {
		t.Fatalf("want shaping present for 150-151, got %+v ok=%v", sh, ok)
	}
	if sh.DelayMs == nil || *sh.DelayMs != 25 {
		t.Fatalf("want A-side delay_ms=25, got %+v", sh)
	}
}

func TestPollNilsShapingWhenNotFlaggedShaped(t *testing.T) {
	// A side: preshaped-at-baseline (shaped=false) -> key present, value nil
	aJSON := `[{"ifid":"6049","neighbor_isd_as":"1-151","link_to":"child","device":"sci9","shaping":{"delay_ms":3,"rate_mbit":10000},"shaped":false}]`
	aSrv, _ := newFakeLinkd(aJSON)
	defer aSrv.Close()
	bSrv, _ := newFakeLinkd(`[]`)
	defer bSrv.Close()
	c := New(testGraph(aSrv.URL, bSrv.URL), testClient())
	got, _ := c.Poll(context.Background())
	sh, ok := got["150-151"]
	if !ok {
		t.Fatal("link key must stay present (nil-not-absent contract)")
	}
	if sh != nil {
		t.Fatalf("shaping = %+v, want nil for shaped=false", sh)
	}
}

func TestPollUnshapedASideIsNilNotAbsent(t *testing.T) {
	aJSON := `[{"ifid":"6049","neighbor_isd_as":"1-151","link_to":"child","device":"sci9","shaping":null}]`
	bJSON := `[{"ifid":"150","neighbor_isd_as":"1-150","link_to":"parent","device":"sci9","shaping":{"delay_ms":5}}]`
	aSrv, _ := newFakeLinkd(aJSON)
	defer aSrv.Close()
	bSrv, _ := newFakeLinkd(bJSON)
	defer bSrv.Close()

	g := testGraph(aSrv.URL, bSrv.URL)
	c := New(g, testClient())

	got, _ := c.Poll(context.Background())
	sh, ok := got["150-151"]
	if !ok {
		t.Fatalf("want key present for 150-151 (A side reachable), got absent")
	}
	if sh != nil {
		t.Fatalf("want nil shaping (A side explicitly unshaped), got %+v", sh)
	}
}

func TestPollSkipsUnreachableAS(t *testing.T) {
	aSrv, _ := newFakeLinkd(`[{"ifid":"6049","shaping":{"delay_ms":5}}]`)
	aSrv.Close() // closed before use: connection refused on every request
	bSrv, _ := newFakeLinkd(`[{"ifid":"150","shaping":null}]`)
	defer bSrv.Close()

	g := testGraph(aSrv.URL, bSrv.URL)
	c := New(g, testClient())

	got, _ := c.Poll(context.Background())
	if _, ok := got["150-151"]; ok {
		t.Fatalf("want no entry for 150-151 when AS150's linkd is unreachable, got %+v", got)
	}
}

func TestApplyBothHitsBothEndpointsWithCorrectBodies(t *testing.T) {
	aSrv, aFake := newFakeLinkd(`[]`)
	defer aSrv.Close()
	bSrv, bFake := newFakeLinkd(`[]`)
	defer bSrv.Close()

	g := testGraph(aSrv.URL, bSrv.URL)
	c := New(g, testClient())

	delay := 50.0
	results := c.Apply(context.Background(), g.Links[0], "both", derive.Shaping{DelayMs: &delay}, false)

	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d: %+v", len(results), results)
	}
	if results[0].AS != 150 || !results[0].OK || results[0].Error != "" {
		t.Fatalf("want AS150 result OK, got %+v", results[0])
	}
	if results[1].AS != 151 || !results[1].OK || results[1].Error != "" {
		t.Fatalf("want AS151 result OK, got %+v", results[1])
	}

	wantBody := `{"delay_ms":50}`
	if reqs := aFake.requests(); len(reqs) != 1 || reqs[0].method != http.MethodPut ||
		reqs[0].path != "/api/v1/links/6049" || reqs[0].body != wantBody {
		t.Fatalf("A-side request wrong: %+v", reqs)
	}
	if reqs := bFake.requests(); len(reqs) != 1 || reqs[0].method != http.MethodPut ||
		reqs[0].path != "/api/v1/links/150" || reqs[0].body != wantBody {
		t.Fatalf("B-side request wrong: %+v", reqs)
	}
}

func TestApplyMixedResultsWhenOneServerFails(t *testing.T) {
	aSrv, aFake := newFakeLinkd(`[]`)
	defer aSrv.Close()
	bSrv, _ := newFakeLinkd(`[]`)
	defer bSrv.Close()
	aFake.setStatus(http.StatusInternalServerError)

	g := testGraph(aSrv.URL, bSrv.URL)
	c := New(g, testClient())

	delay := 10.0
	results := c.Apply(context.Background(), g.Links[0], "both", derive.Shaping{DelayMs: &delay}, false)

	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d: %+v", len(results), results)
	}
	if results[0].AS != 150 || results[0].OK || results[0].Error == "" {
		t.Fatalf("want AS150 result to fail with a message, got %+v", results[0])
	}
	if results[1].AS != 151 || !results[1].OK {
		t.Fatalf("want AS151 result OK, got %+v", results[1])
	}
}

func TestApplyClearSendsDeleteToASideOnly(t *testing.T) {
	aSrv, aFake := newFakeLinkd(`[]`)
	defer aSrv.Close()
	bSrv, bFake := newFakeLinkd(`[]`)
	defer bSrv.Close()

	g := testGraph(aSrv.URL, bSrv.URL)
	c := New(g, testClient())

	results := c.Apply(context.Background(), g.Links[0], "a_to_b", derive.Shaping{}, true)

	if len(results) != 1 || results[0].AS != 150 || !results[0].OK {
		t.Fatalf("want single OK result for AS150, got %+v", results)
	}
	if reqs := aFake.requests(); len(reqs) != 1 || reqs[0].method != http.MethodDelete || reqs[0].path != "/api/v1/links/6049" {
		t.Fatalf("want single DELETE /api/v1/links/6049 on A, got %+v", reqs)
	}
	if reqs := bFake.requests(); len(reqs) != 0 {
		t.Fatalf("want no requests reaching B, got %+v", reqs)
	}
}

func TestAllHealth(t *testing.T) {
	aSrv, _ := newFakeLinkd(`[]`)
	defer aSrv.Close()
	bSrv, bFake := newFakeLinkd(`[]`)
	defer bSrv.Close()
	bFake.setStatus(http.StatusInternalServerError)

	g := testGraph(aSrv.URL, bSrv.URL)
	c := New(g, testClient())

	got := c.AllHealth(context.Background())
	if !got[150] {
		t.Fatalf("want AS150 healthy, got %+v", got)
	}
	if got[151] {
		t.Fatalf("want AS151 unhealthy, got %+v", got)
	}
}

func TestPollBGP(t *testing.T) {
	// Two-AS graph, one link; AS A serves a session, AS B returns 503.
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/bgp" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"sessions":[{"ifid":"6049","state":"Established","bfd":"Up","since_unix":1770000000}],"routes":[{"prefix_as":150,"ifid":"6049"}]}`)
	}))
	defer up.Close()
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bgp unavailable"}`, http.StatusServiceUnavailable)
	}))
	defer down.Close()

	g := testGraph(up.URL, down.URL) // A=AS150 ifid 6049, B=AS151
	c := New(g, testClient())

	got, routes := c.PollBGP(context.Background())
	bl := got[g.Links[0].ID]
	if bl == nil || bl.A == nil || bl.A.State != "Established" || bl.A.SinceUnix != 1770000000 {
		t.Fatalf("A side: %+v", bl)
	}
	if bl.B != nil {
		t.Fatalf("B side must be nil (503), got %+v", bl.B)
	}

	// AS150 (up) must have its routes decoded; AS151 (503) must be absent
	// entirely from the routes map, not present with an empty value.
	if r, ok := routes[150]; !ok || len(r) != 1 || r[150] != "6049" {
		t.Fatalf("routes[150] = %+v (ok=%v), want {150:\"6049\"}", r, ok)
	}
	if _, ok := routes[151]; ok {
		t.Fatalf("routes[151] must be absent (erroring AS), got %+v", routes[151])
	}
}
