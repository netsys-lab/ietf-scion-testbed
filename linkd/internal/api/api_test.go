package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/shape"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/topo"
)

type fakeShaper struct{ state map[string]shape.Params }

func (f *fakeShaper) Get(dev string) (shape.Params, error) { return f.state[dev], nil }
func (f *fakeShaper) Apply(dev string, p shape.Params) error {
	f.state[dev] = p
	return nil
}
func (f *fakeShaper) Clear(dev string) error {
	delete(f.state, dev)
	return nil
}

func newTestServer(opts Options) (*httptest.Server, *fakeShaper) {
	fs := &fakeShaper{state: map[string]shape.Params{}}
	ifs := []ManagedIface{{
		Interface: topo.Interface{IfID: "6049", Neighbor: "1-151", LinkTo: "parent",
			LocalIP: netip.MustParseAddr("fd00:fade:9::155")},
		Dev: "sci9",
	}}
	return httptest.NewServer(New(ifs, fs, opts)), fs
}

func TestListLinks(t *testing.T) {
	srv, _ := newTestServer(Options{})
	defer srv.Close()
	res, err := srv.Client().Get(srv.URL + "/api/v1/links")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("status %v err %v", res.StatusCode, err)
	}
	var got []map[string]any
	json.NewDecoder(res.Body).Decode(&got)
	if len(got) != 1 || got[0]["ifid"] != "6049" || got[0]["device"] != "sci9" {
		t.Fatalf("got %+v", got)
	}
	if got[0]["shaping"] != nil {
		t.Fatalf("expected null shaping, got %v", got[0]["shaping"])
	}
}

func doJSON(t *testing.T, srv *httptest.Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, srv.URL+path, strings.NewReader(body))
	rr := httptest.NewRecorder()
	// route through the actual handler
	srv.Config.Handler.ServeHTTP(rr, req)
	return rr
}

func TestPutMergeAndValidate(t *testing.T) {
	srv, fs := newTestServer(Options{})
	defer srv.Close()
	if rr := doJSON(t, srv, "PUT", "/api/v1/links/6049", `{"delay_ms":50}`); rr.Code != 200 {
		t.Fatalf("apply: %d %s", rr.Code, rr.Body)
	}
	if rr := doJSON(t, srv, "PUT", "/api/v1/links/6049", `{"loss_pct":1}`); rr.Code != 200 {
		t.Fatalf("merge: %d %s", rr.Code, rr.Body)
	}
	got := fs.state["sci9"]
	if got.DelayMs == nil || *got.DelayMs != 50 || got.LossPct == nil || *got.LossPct != 1 {
		t.Fatalf("merge lost fields: %+v", got)
	}
	if rr := doJSON(t, srv, "PUT", "/api/v1/links/6049", `{"delay_ms":9999}`); rr.Code != 400 {
		t.Fatalf("want 400, got %d", rr.Code)
	}
	if rr := doJSON(t, srv, "PUT", "/api/v1/links/999", `{"delay_ms":1}`); rr.Code != 404 {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

func TestDeleteAndHealth(t *testing.T) {
	srv, fs := newTestServer(Options{})
	defer srv.Close()
	fs.state["sci9"] = shape.Params{}
	if rr := doJSON(t, srv, "DELETE", "/api/v1/links/6049", ""); rr.Code != 200 {
		t.Fatalf("delete: %d", rr.Code)
	}
	if _, ok := fs.state["sci9"]; ok {
		t.Fatal("state not cleared")
	}
	res, _ := srv.Client().Get(srv.URL + "/healthz")
	if res.StatusCode != 200 {
		t.Fatalf("healthz %d", res.StatusCode)
	}
}

func f64(v float64) *float64 { return &v }

func listJSON(t *testing.T, srv *httptest.Server) []map[string]any {
	t.Helper()
	res, err := srv.Client().Get(srv.URL + "/api/v1/links")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("list: %v %v", res.StatusCode, err)
	}
	var got []map[string]any
	json.NewDecoder(res.Body).Decode(&got)
	return got
}

func TestDeleteRestoresBaseline(t *testing.T) {
	bl := map[string]shape.Params{"6049": {DelayMs: f64(3), RateMbit: f64(500)}}
	var changed int
	srv, fs := newTestServer(Options{Baseline: bl, OnChange: func() { changed++ }})
	defer srv.Close()
	fs.state["sci9"] = shape.Params{DelayMs: f64(60)}
	if rr := doJSON(t, srv, "DELETE", "/api/v1/links/6049", ""); rr.Code != 200 {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body)
	}
	got := fs.state["sci9"]
	if got.DelayMs == nil || *got.DelayMs != 3 || got.RateMbit == nil || *got.RateMbit != 500 {
		t.Fatalf("baseline not applied: %+v", got)
	}
	if changed != 1 {
		t.Fatalf("OnChange calls = %d, want 1", changed)
	}
}

func TestDeleteWithoutBaselineClears(t *testing.T) {
	srv, fs := newTestServer(Options{})
	defer srv.Close()
	fs.state["sci9"] = shape.Params{DelayMs: f64(9)}
	if rr := doJSON(t, srv, "DELETE", "/api/v1/links/6049", ""); rr.Code != 200 {
		t.Fatalf("delete: %d", rr.Code)
	}
	if _, ok := fs.state["sci9"]; ok {
		t.Fatal("state not cleared")
	}
}

func TestListReportsBaselineAndShaped(t *testing.T) {
	bl := map[string]shape.Params{"6049": {DelayMs: f64(3), RateMbit: f64(500)}}
	srv, fs := newTestServer(Options{Baseline: bl})
	defer srv.Close()
	// at baseline (kernel rounding tolerated) -> shaped=false
	fs.state["sci9"] = shape.Params{DelayMs: f64(3.0), RateMbit: f64(499.9)}
	got := listJSON(t, srv)
	if got[0]["shaped"] != false {
		t.Fatalf("shaped = %v, want false at baseline", got[0]["shaped"])
	}
	if got[0]["baseline"] == nil {
		t.Fatal("baseline missing from list")
	}
	// deviating -> shaped=true
	fs.state["sci9"] = shape.Params{DelayMs: f64(60), RateMbit: f64(500)}
	if got = listJSON(t, srv); got[0]["shaped"] != true {
		t.Fatalf("shaped = %v, want true when deviating", got[0]["shaped"])
	}
}

func TestPutCallsOnChange(t *testing.T) {
	var changed int
	srv, _ := newTestServer(Options{OnChange: func() { changed++ }})
	defer srv.Close()
	if rr := doJSON(t, srv, "PUT", "/api/v1/links/6049", `{"delay_ms":42}`); rr.Code != 200 {
		t.Fatalf("put: %d", rr.Code)
	}
	if changed != 1 {
		t.Fatalf("OnChange calls = %d, want 1", changed)
	}
}

func TestHealthReportsMetadataStatus(t *testing.T) {
	srv, _ := newTestServer(Options{Status: func() (bool, bool) { return true, false }})
	defer srv.Close()
	res, _ := srv.Client().Get(srv.URL + "/healthz")
	var body map[string]any
	json.NewDecoder(res.Body).Decode(&body)
	if body["metadata_ok"] != true || body["reload_ok"] != false {
		t.Fatalf("health = %v", body)
	}
}
