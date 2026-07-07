package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/idint"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
)

// fakeProber is a settable-func idint.Prober for api-layer tests. Unset
// funcs return a zero-value response with no error (tests that never
// exercise Probe/Paths, e.g. plain Set/Clear, don't need to wire them).
type fakeProber struct {
	pathsFn func(ctx context.Context, src, dst int) (*idint.PathsResponse, error)
	probeFn func(ctx context.Context, src, dst int, fingerprint string) (*idint.ProbeResult, error)
}

func (f *fakeProber) Paths(ctx context.Context, src, dst int) (*idint.PathsResponse, error) {
	if f.pathsFn == nil {
		return &idint.PathsResponse{}, nil
	}
	return f.pathsFn(ctx, src, dst)
}

func (f *fakeProber) Probe(ctx context.Context, src, dst int, fingerprint string) (*idint.ProbeResult, error) {
	if f.probeFn == nil {
		return &idint.ProbeResult{}, nil
	}
	return f.probeFn(ctx, src, dst, fingerprint)
}

// goodProbeResult is a valid 155->160 probe result over the fixture's sole
// paired link (155-160, ifids 36530/39652; see ../topo/testdata).
func goodProbeResult() *idint.ProbeResult {
	return &idint.ProbeResult{
		Path: idint.PathJSON{
			Fingerprint: "fp-good",
			Interfaces: []idint.IfaceJSON{
				{IA: "1-155", IfID: 36530},
				{IA: "1-160", IfID: 39652},
			},
			LatencyUs: []int64{1000},
		},
		ProbeRttMs: 5,
		Fwd: []idint.HopRecord{
			{Hop: 0, IA: "1-155", Source: true},
			{Hop: 1, IA: "1-160", Egress: true},
		},
	}
}

// newIdintServer builds a server with a live idint.Manager wrapping p. The
// interval is irrelevant to these tests: they drive TickOnce directly rather
// than waiting on Run's ticker.
func newIdintServer(t *testing.T, p idint.Prober, jc JoinConfig) http.Handler {
	t.Helper()
	g := loadGraph(t)
	st := store.New(60)
	d := derive.New(g, st)
	tr := idint.NewManager(g, p, time.Hour)
	return New(g, st, d, &fakeController{health: map[int]bool{}, shaping: map[string]*derive.Shaping{}}, nil, jc, nil, tr)
}

// --- Case 1: disabled (nil manager) -> all four routes 404 ---

func TestIdintDisabledAll404(t *testing.T) {
	h, _, _, _ := newTestServer(t, nil) // newTestServer's tr is always nil
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/idint/paths?src=155&dst=160"},
		{http.MethodPut, "/api/idint/trace"},
		{http.MethodDelete, "/api/idint/trace"},
		{http.MethodGet, "/api/idint/trace"},
	}
	for _, c := range cases {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`)))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s %s: want 404 while disabled, got %d", c.method, c.path, rr.Code)
		}
	}
}

// --- Case 2: happy path — PUT set, GET shows vm, DELETE clears it ---

func TestIdintTraceSetGetClear(t *testing.T) {
	h := newIdintServer(t, &fakeProber{}, JoinConfig{})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/idint/trace",
		strings.NewReader(`{"src":155,"dst":160,"fingerprint":""}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT trace: want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var okResp map[string]bool
	if err := json.Unmarshal(rr.Body.Bytes(), &okResp); err != nil || !okResp["ok"] {
		t.Fatalf("PUT trace: body = %s, err = %v", rr.Body.String(), err)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/idint/trace", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET trace: want 200, got %d", rr.Code)
	}
	var traceResp struct {
		VM     *derive.TraceVM     `json:"vm"`
		Latest *idint.ProbeResult `json:"latest"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &traceResp); err != nil {
		t.Fatalf("decode GET trace: %v (%s)", err, rr.Body.String())
	}
	if traceResp.VM == nil {
		t.Fatalf("GET trace: vm nil after Set")
	}
	if traceResp.VM.Src != "1-155" || traceResp.VM.Dst != "1-160" {
		t.Fatalf("vm src/dst = %q/%q, want 1-155/1-160", traceResp.VM.Src, traceResp.VM.Dst)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, "/api/idint/trace", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE trace: want 200, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/idint/trace", nil))
	traceResp.VM = nil
	if err := json.Unmarshal(rr.Body.Bytes(), &traceResp); err != nil {
		t.Fatalf("decode GET trace after clear: %v", err)
	}
	if traceResp.VM != nil {
		t.Fatalf("vm after DELETE = %+v, want nil", traceResp.VM)
	}
}

// --- Case 3: validation and upstream failures ---

func TestIdintBadRequests(t *testing.T) {
	p := &fakeProber{
		pathsFn: func(ctx context.Context, src, dst int) (*idint.PathsResponse, error) {
			return nil, errors.New("prober unreachable")
		},
	}
	h := newIdintServer(t, p, JoinConfig{})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/idint/trace",
		strings.NewReader(`{"src":999,"dst":160,"fingerprint":""}`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("PUT unknown AS: want 400, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/idint/trace", strings.NewReader(`not json`)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("PUT bad JSON: want 400, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/idint/paths?src=abc&dst=160", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("GET paths non-int src: want 400, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/idint/paths?src=999&dst=160", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("GET paths unknown src (ErrBadSession): want 400, got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/idint/paths?src=155&dst=160", nil))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("GET paths prober failure: want 502, got %d: %s", rr.Code, rr.Body.String())
	}
	var errResp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &errResp); err != nil || errResp["error"] == "" {
		t.Fatalf("502 body missing error: %s (err=%v)", rr.Body.String(), err)
	}
}

// --- Case 4: Frame.Trace marshaling + Manager wiring through the api layer ---

func TestIdintFrameAttach(t *testing.T) {
	f := derive.Frame{}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	if strings.Contains(string(data), `"trace"`) {
		t.Fatalf("Frame with nil Trace must omit \"trace\": %s", data)
	}

	f.Trace = &derive.TraceVM{Src: "1-155", Dst: "1-160", Ok: true}
	data, err = json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal frame with trace: %v", err)
	}
	if !strings.Contains(string(data), `"trace"`) {
		t.Fatalf("Frame with Trace set must include \"trace\": %s", data)
	}

	p := &fakeProber{probeFn: func(ctx context.Context, src, dst int, fingerprint string) (*idint.ProbeResult, error) {
		return goodProbeResult(), nil
	}}
	h := newIdintServer(t, p, JoinConfig{})
	s, ok := h.(*server)
	if !ok {
		t.Fatalf("New did not return *server")
	}
	if s.tr.VM() != nil {
		t.Fatalf("VM() non-nil before Set")
	}
	if err := s.tr.Set(155, 160, ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	s.tr.TickOnce(context.Background())
	if s.tr.VM() == nil {
		t.Fatalf("VM() nil after Set + TickOnce")
	}
}

// --- Case 5: booth-code gate applies to /api/idint routes too ---

func TestIdintBoothGate(t *testing.T) {
	h := newIdintServer(t, &fakeProber{}, JoinConfig{BoothCode: "x"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/idint/trace", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /api/idint/trace: want 401, got %d", rr.Code)
	}
}
