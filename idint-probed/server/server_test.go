package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/idint-probed/prober"
	"github.com/netsys-lab/ietf-scion-testbed/idint-probed/server"
	"github.com/scionproto/scion/pkg/addr"
	"github.com/scionproto/scion/pkg/snet"
)

// fakeEngine is a canned prober.Engine for the HTTP layer tests.
type fakeEngine struct {
	pathsResp *prober.PathsResponse
	pathsErr  error
	probeRes  *prober.ProbeResult
	probeErr  error

	// If set, Probe signals on entered and then blocks until release is
	// closed (used by the 409 test).
	entered chan struct{}
	release chan struct{}
}

func (f *fakeEngine) Paths(ctx context.Context, dst addr.IA) (*prober.PathsResponse, error) {
	return f.pathsResp, f.pathsErr
}

func (f *fakeEngine) Probe(
	ctx context.Context, remote *snet.UDPAddr, fingerprint string,
) (*prober.ProbeResult, error) {
	if f.entered != nil {
		f.entered <- struct{}{}
		<-f.release
	}
	return f.probeRes, f.probeErr
}

func newTestServer(t *testing.T, eng prober.Engine) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer((&server.Server{Engine: eng}).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func postProbe(t *testing.T, url string, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(
		url+"/api/v1/probe", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST /api/v1/probe: %v", err)
	}
	return resp
}

func decodeErrBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("error body is not JSON: %v", err)
	}
	msg, ok := m["error"]
	if !ok {
		t.Fatalf(`error body missing "error" key: %v`, m)
	}
	return msg
}

func TestPathsOK(t *testing.T) {
	want := &prober.PathsResponse{
		LocalIA: "1-150",
		Paths: []prober.PathJSON{{
			Fingerprint: "abc123",
			MTU:         1472,
			Expiry:      "2026-07-07T12:00:00Z",
			Interfaces: []prober.IfaceJSON{
				{IA: "1-150", IfID: 1},
				{IA: "1-154", IfID: 2},
			},
			LatencyUs: []int64{5000},
		}},
	}
	ts := newTestServer(t, &fakeEngine{pathsResp: want})

	resp, err := http.Get(ts.URL + "/api/v1/paths?dst=1-154")
	if err != nil {
		t.Fatalf("GET /api/v1/paths: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got prober.PathsResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if !reflect.DeepEqual(&got, want) {
		t.Errorf("body = %+v, want %+v", got, *want)
	}
}

func TestPathsBadDst(t *testing.T) {
	ts := newTestServer(t, &fakeEngine{})

	resp, err := http.Get(ts.URL + "/api/v1/paths?dst=not-an-ia")
	if err != nil {
		t.Fatalf("GET /api/v1/paths: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if msg := decodeErrBody(t, resp); msg == "" {
		t.Error("error message is empty")
	}
}

func TestProbeOK(t *testing.T) {
	nodeID := uint32(2)
	want := &prober.ProbeResult{
		Path:       prober.PathJSON{Fingerprint: "abc123", MTU: 1472},
		ProbeRttMs: 3.25,
		Fwd: []prober.HopRecord{
			{Hop: 0, IA: "1-150", Source: true, Verified: true},
			{Hop: 1, IA: "1-154", Egress: true, Verified: true, NodeId: &nodeID},
		},
		Rev: []prober.HopRecord{
			{Hop: 0, IA: "1-154", Source: true, Verified: true},
		},
	}
	ts := newTestServer(t, &fakeEngine{probeRes: want})

	resp := postProbe(t, ts.URL, `{"remote":"1-154,10.20.3.154:40001","fingerprint":"abc123"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got prober.ProbeResult
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if !reflect.DeepEqual(&got, want) {
		t.Errorf("body = %+v, want %+v", got, *want)
	}
}

func TestProbeFingerprintNotFound(t *testing.T) {
	ts := newTestServer(t, &fakeEngine{probeErr: prober.ErrFingerprintNotFound})

	resp := postProbe(t, ts.URL, `{"remote":"1-154,10.20.3.154:40001","fingerprint":"nope"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if msg := decodeErrBody(t, resp); msg != "fingerprint not found" {
		t.Errorf("error = %q, want %q", msg, "fingerprint not found")
	}
}

func TestProbeTimeout(t *testing.T) {
	ts := newTestServer(t, &fakeEngine{probeErr: prober.ErrTimeout})

	resp := postProbe(t, ts.URL, `{"remote":"1-154,10.20.3.154:40001"}`)
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", resp.StatusCode)
	}
	if msg := decodeErrBody(t, resp); msg != "probe timeout" {
		t.Errorf("error = %q, want %q", msg, "probe timeout")
	}
}

func TestProbeConflictWhileInFlight(t *testing.T) {
	eng := &fakeEngine{
		probeRes: &prober.ProbeResult{},
		entered:  make(chan struct{}, 1),
		release:  make(chan struct{}),
	}
	ts := newTestServer(t, eng)

	// First probe: blocks inside Engine.Probe while holding the mutex.
	firstDone := make(chan error, 1)
	go func() {
		resp := postProbe(t, ts.URL, `{"remote":"1-154,10.20.3.154:40001"}`)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			firstDone <- fmt.Errorf("first probe status = %d, want 200", resp.StatusCode)
			return
		}
		firstDone <- nil
	}()

	select {
	case <-eng.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first probe never reached Engine.Probe")
	}

	// Second probe while the first holds the mutex: 409.
	resp := postProbe(t, ts.URL, `{"remote":"1-154,10.20.3.154:40001"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if msg := decodeErrBody(t, resp); msg != "probe in flight" {
		t.Errorf("error = %q, want %q", msg, "probe in flight")
	}

	close(eng.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestProbeBadRemote(t *testing.T) {
	ts := newTestServer(t, &fakeEngine{})

	resp := postProbe(t, ts.URL, `{"remote":"not a scion address"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if msg := decodeErrBody(t, resp); msg == "" {
		t.Error("error message is empty")
	}
}
