package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/linkdclient"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// fixtureLink is the only fully-paired link in internal/topo/testdata.
const fixtureLink = "155-160"

// fakeController is an injectable stand-in for *linkdclient.Client.
type fakeController struct {
	results []linkdclient.Result
	health  map[int]bool
	shaping map[string]*derive.Shaping
	bgp     map[string]*derive.BGPLink

	lastDirection string
	lastClear     bool
	lastShaping   derive.Shaping
	baseline      map[string]*derive.Shaping
}

func (f *fakeController) Poll(ctx context.Context) (shaping, baseline map[string]*derive.Shaping) {
	return f.shaping, f.baseline
}

func (f *fakeController) PollBGP(ctx context.Context) map[string]*derive.BGPLink {
	return f.bgp
}

func (f *fakeController) Apply(ctx context.Context, link topo.Link, direction string, p derive.Shaping, clear bool) []linkdclient.Result {
	f.lastDirection = direction
	f.lastClear = clear
	f.lastShaping = p
	return f.results
}

func (f *fakeController) AllHealth(ctx context.Context) map[int]bool {
	return f.health
}

func loadGraph(t *testing.T) topo.Graph {
	t.Helper()
	g, err := topo.Load("../topo/testdata")
	if err != nil {
		t.Fatalf("load fixture topology: %v", err)
	}
	return g
}

func newTestServer(t *testing.T, lc Controller) (http.Handler, *store.Store, *derive.Deriver, topo.Graph) {
	t.Helper()
	g := loadGraph(t)
	st := store.New(60)
	d := derive.New(g, st)
	if lc == nil {
		lc = &fakeController{health: map[int]bool{}, shaping: map[string]*derive.Shaping{}}
	}
	return New(g, st, d, lc, nil, JoinConfig{}, nil, nil), st, d, g
}

func TestTopology(t *testing.T) {
	h, _, _, _ := newTestServer(t, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/topology", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var g topo.Graph
	if err := json.Unmarshal(rr.Body.Bytes(), &g); err != nil {
		t.Fatalf("decode graph: %v", err)
	}
	if !hasLink(g, fixtureLink) {
		t.Fatalf("topology missing %s link: %+v", fixtureLink, g.Links)
	}
}

func TestHistory(t *testing.T) {
	h, st, _, _ := newTestServer(t, nil)
	now := time.Now().UnixMilli()
	st.Put("155/br/rtt/36530", now-1000, 5)
	st.Put("155/br/rtt/36530", now, 6)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/history?key=155/br/rtt/36530&mins=15", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var samples []store.Sample
	if err := json.Unmarshal(rr.Body.Bytes(), &samples); err != nil {
		t.Fatalf("decode samples: %v (%s)", err, rr.Body.String())
	}
	if len(samples) != 2 || samples[1].V != 6 {
		t.Fatalf("want 2 samples ending at v=6, got %+v", samples)
	}
	// wire format must be [{"t":..,"v":..}]
	if !strings.Contains(rr.Body.String(), `"t":`) || !strings.Contains(rr.Body.String(), `"v":`) {
		t.Fatalf("history wire format wrong: %s", rr.Body.String())
	}
}

func TestHistoryUnknownKeyIsEmptyArray(t *testing.T) {
	h, _, _, _ := newTestServer(t, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/history?key=nope", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Fatalf("want [] for unknown key, got %q", got)
	}
}

func TestShapingUnknownLink(t *testing.T) {
	h, _, _, _ := newTestServer(t, nil)
	body := `{"direction":"both","delay_ms":50}`
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/links/999-999/shaping", strings.NewReader(body)))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404 for unknown link, got %d", rr.Code)
	}
}

func TestShapingBadDirection(t *testing.T) {
	h, _, _, _ := newTestServer(t, nil)
	body := `{"direction":"sideways","delay_ms":50}`
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/links/"+fixtureLink+"/shaping", strings.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad direction, got %d", rr.Code)
	}
}

func TestShapingMixedResultsIs200(t *testing.T) {
	lc := &fakeController{results: []linkdclient.Result{
		{AS: 155, OK: true},
		{AS: 160, OK: false, Error: "boom"},
	}}
	h, _, _, _ := newTestServer(t, lc)

	body := `{"direction":"both","delay_ms":50,"loss_pct":1.5}`
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/links/"+fixtureLink+"/shaping", strings.NewReader(body)))

	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 for mixed results, got %d", rr.Code)
	}
	var resp struct {
		Results []linkdclient.Result `json:"results"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode results: %v", err)
	}
	if len(resp.Results) != 2 || !resp.Results[0].OK || resp.Results[1].OK || resp.Results[1].Error != "boom" {
		t.Fatalf("results not echoed: %+v", resp.Results)
	}
	if lc.lastDirection != "both" || lc.lastClear {
		t.Fatalf("controller called wrong: dir=%s clear=%v", lc.lastDirection, lc.lastClear)
	}
	if lc.lastShaping.DelayMs == nil || *lc.lastShaping.DelayMs != 50 {
		t.Fatalf("shaping delay not forwarded: %+v", lc.lastShaping)
	}
}

func TestShapingAllFailedIs502(t *testing.T) {
	lc := &fakeController{results: []linkdclient.Result{
		{AS: 155, OK: false, Error: "x"},
		{AS: 160, OK: false, Error: "y"},
	}}
	h, _, _, _ := newTestServer(t, lc)

	body := `{"direction":"both","delay_ms":50}`
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPut, "/api/links/"+fixtureLink+"/shaping", strings.NewReader(body)))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("want 502 when all results fail, got %d", rr.Code)
	}
}

func TestReset(t *testing.T) {
	lc := &fakeController{results: []linkdclient.Result{{AS: 155, OK: true}, {AS: 160, OK: true}}}
	h, _, _, _ := newTestServer(t, lc)

	body := `{"direction":"both"}`
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/links/"+fixtureLink+"/reset", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 for reset, got %d", rr.Code)
	}
	if !lc.lastClear {
		t.Fatalf("reset must call Apply with clear=true")
	}
}

func TestResetBadDirection(t *testing.T) {
	h, _, _, _ := newTestServer(t, nil)
	body := `{"direction":"nope"}`
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/links/"+fixtureLink+"/reset", strings.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad reset direction, got %d", rr.Code)
	}
}

func TestHealth(t *testing.T) {
	lc := &fakeController{health: map[int]bool{155: true, 160: false}}
	h, st, _, _ := newTestServer(t, lc)
	now := time.Now().UnixMilli()
	st.Put("155/br/_up/", now, 1)
	st.Put("155/cs/_up/", now, 0)
	st.Put("160/sd/_up/", now, 1)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var resp struct {
		Targets map[string]bool `json:"targets"`
		Linkd   map[string]bool `json:"linkd"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode health: %v (%s)", err, rr.Body.String())
	}
	if v, ok := resp.Targets["155/br"]; !ok || !v {
		t.Fatalf("want targets[155/br]=true, got %+v", resp.Targets)
	}
	if v, ok := resp.Targets["155/cs"]; !ok || v {
		t.Fatalf("want targets[155/cs]=false, got %+v", resp.Targets)
	}
	if v, ok := resp.Linkd["155"]; !ok || !v {
		t.Fatalf("want linkd[155]=true, got %+v", resp.Linkd)
	}
	if v, ok := resp.Linkd["160"]; !ok || v {
		t.Fatalf("want linkd[160]=false, got %+v", resp.Linkd)
	}
}

func TestLiveSnapshotAndFrame(t *testing.T) {
	h, _, _, _ := newTestServer(t, nil)
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go RunBroadcast(ctx, h, 20*time.Millisecond, time.Second)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/live"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}
	defer conn.Close()

	// First message must be the snapshot with the full topology.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var snap struct {
		Type     string       `json:"type"`
		Topology topo.Graph   `json:"topology"`
		Frame    derive.Frame `json:"frame"`
	}
	if err := conn.ReadJSON(&snap); err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if snap.Type != "snapshot" {
		t.Fatalf("want type=snapshot, got %q", snap.Type)
	}
	if !hasLink(snap.Topology, fixtureLink) {
		t.Fatalf("snapshot topology missing %s: %+v", fixtureLink, snap.Topology.Links)
	}

	// Then at least one broadcast frame.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var fr struct {
		Type  string       `json:"type"`
		Frame derive.Frame `json:"frame"`
	}
	if err := conn.ReadJSON(&fr); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if fr.Type != "frame" {
		t.Fatalf("want type=frame, got %q", fr.Type)
	}
}

func TestStaticFallback(t *testing.T) {
	g := loadGraph(t)
	st := store.New(60)
	d := derive.New(g, st)
	lc := &fakeController{}
	static := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html>INDEX")},
		"app.js":     {Data: []byte("// APP")},
	}
	h := New(g, st, d, lc, static, JoinConfig{}, nil, nil)

	// Known asset served verbatim.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "APP") {
		t.Fatalf("want app.js served, got %d %q", rr.Code, rr.Body.String())
	}

	// Unknown non-/api path falls back to index.html (SPA).
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/deep/route", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "INDEX") {
		t.Fatalf("want index.html fallback, got %d %q", rr.Code, rr.Body.String())
	}

	// Unknown /api path must not fall back to index.html.
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/nope", nil))
	if rr.Code == http.StatusOK && strings.Contains(rr.Body.String(), "INDEX") {
		t.Fatalf("unknown /api path must not serve index.html")
	}
}

func hasLink(g topo.Graph, id string) bool {
	for _, l := range g.Links {
		if l.ID == id {
			return true
		}
	}
	return false
}
