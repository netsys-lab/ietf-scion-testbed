package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/wgpool"
)

// Disabled join surface must be invisible: every join route 404s.
func TestJoinDisabledAll404(t *testing.T) {
	h, _, _, _ := newTestServer(t, nil) // newTestServer passes JoinConfig{} (disabled)
	for _, u := range []string{"/api/join/meta", "/api/join/bundle/158"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, u, nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s: want 404 while disabled, got %d", u, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/join/claim", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("claim: want 404 while disabled, got %d", rr.Code)
	}
}

func TestASAllowed(t *testing.T) {
	jc := JoinConfig{JoinableASes: []int{158, 159}}
	if !jc.asAllowed(158) || jc.asAllowed(150) {
		t.Fatal("asAllowed wrong")
	}
}

// newJoinServerWithConfig builds a join-enabled server from an
// attendee-supplied JoinConfig, backed by a fresh temp-file wgpool with the
// given number of slots. It's the common setup every join test shares; jc
// lets a test override JoinableASes / BootstrapURLTemplate / etc. without
// duplicating the pool-file and graph plumbing.
func newJoinServerWithConfig(t *testing.T, slots int, jc JoinConfig) http.Handler {
	t.Helper()
	dir := t.TempDir()
	pf := fmt.Sprintf(`{"server_public_key":"SPUB","listen_port":51820,"slots":[%s]}`,
		strings.Join(func() []string {
			var out []string
			for i := 0; i < slots; i++ {
				n := i + 2
				out = append(out, fmt.Sprintf(`{"n":%d,"ip":"10.20.5.%d","private_key":"PRIV%d","public_key":"PUB%d"}`, n, n, n, n))
			}
			return out
		}(), ","))
	os.WriteFile(filepath.Join(dir, "pool.json"), []byte(pf), 0o600)
	pool, err := wgpool.Open(filepath.Join(dir, "pool.json"), filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	g := loadGraph(t)
	st := store.New(60)
	d := derive.New(g, st)
	return New(g, st, d, &fakeController{health: map[int]bool{}, shaping: map[string]*derive.Shaping{}}, nil, jc, pool, nil)
}

func newJoinServer(t *testing.T, slots int) http.Handler {
	t.Helper()
	return newJoinServerWithConfig(t, slots, JoinConfig{
		Enabled: true, BoothCode: "secret", ISD: 1,
		JoinableASes: []int{158, 159, 160, 161},
		EndpointV6:   "fd99::201", EndpointV4: "203.0.113.7", ListenPort: 51820,
		RateMax: 100, RateWindow: time.Minute,
	})
}

func postClaim(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/join/claim", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.9:1234"
	req.SetBasicAuth("scion", "secret")
	h.ServeHTTP(rr, req)
	return rr
}

func TestClaimHappyPath(t *testing.T) {
	h := newJoinServer(t, 2)
	rr := postClaim(t, h, `{"as":158,"code":"secret"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["ip"] != "10.20.5.2" || resp["isd_as"] != "1-158" {
		t.Fatalf("resp: %v", resp)
	}
	if resp["fc00_identity"] != "fc00:1000:9e00::ffff:a14:502" {
		t.Fatalf("fc00: %v", resp["fc00_identity"])
	}
	conf := resp["conf"].(string)
	for _, want := range []string{"PrivateKey = PRIV2", "Address = 10.20.5.2/32, fd00:beef:5::2/128",
		"DNS = 10.20.3.216", "MTU = 1380", "PublicKey = SPUB",
		"AllowedIPs = 10.20.3.0/24, 10.20.5.0/24, 10.150.0.0/16, 10.151.0.0/16, 10.152.0.0/16, 10.153.0.0/16, 10.154.0.0/16, 10.155.0.0/16, 10.156.0.0/16, 10.157.0.0/16, 10.158.0.0/16, 10.159.0.0/16, 10.160.0.0/16, 10.161.0.0/16, fd00:beef::/32",
		"Endpoint = [fd99::201]:51820", "PersistentKeepalive = 25"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("conf missing %q:\n%s", want, conf)
		}
	}
	if !strings.Contains(resp["conf_v4"].(string), "Endpoint = 203.0.113.7:51820") {
		t.Fatalf("conf_v4 endpoint wrong")
	}
}

// TestRenderConfDNS pins the DNS line's position: it must sit between
// Address and MTU in the [Interface] block, pointing attendees at the
// testbed's SCION-aware resolver.
func TestRenderConfDNS(t *testing.T) {
	sl := wgpool.Slot{N: 2, IP: "10.20.5.2", PrivateKey: "PRIV2", PublicKey: "PUB2"}
	conf := renderConf(sl, "SPUB", "[fd99::201]:51820")
	want := "Address = 10.20.5.2/32, fd00:beef:5::2/128\nDNS = 10.20.3.216\nMTU = 1380\n"
	if !strings.Contains(conf, want) {
		t.Fatalf("conf missing DNS line between Address and MTU:\n%s", conf)
	}
}

func TestClaimBadCode403(t *testing.T) {
	h := newJoinServer(t, 1)
	if rr := postClaim(t, h, `{"as":158,"code":"wrong"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rr.Code)
	}
}

func TestClaimNonJoinable404(t *testing.T) {
	h := newJoinServer(t, 1)
	if rr := postClaim(t, h, `{"as":150,"code":"secret"}`); rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

func TestClaimExhausted409(t *testing.T) {
	h := newJoinServer(t, 1)
	postClaim(t, h, `{"as":158,"code":"secret"}`)
	if rr := postClaim(t, h, `{"as":158,"code":"secret"}`); rr.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", rr.Code)
	}
}

func TestMetaNoCodeNeeded(t *testing.T) {
	h := newJoinServer(t, 2)
	postClaim(t, h, `{"as":158,"code":"secret"}`)
	rr := httptest.NewRecorder()
	metaReq := httptest.NewRequest(http.MethodGet, "/api/join/meta", nil)
	metaReq.SetBasicAuth("scion", "secret")
	h.ServeHTTP(rr, metaReq)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var m map[string]any
	json.Unmarshal(rr.Body.Bytes(), &m)
	if m["slots_total"].(float64) != 2 || m["slots_claimed"].(float64) != 1 {
		t.Fatalf("meta: %v", m)
	}
	if m["endpoint_v6"] != "[fd99::201]:51820" {
		t.Fatalf("endpoint_v6: %v", m["endpoint_v6"])
	}
}

func TestMetaJoinableInfo(t *testing.T) {
	h := newJoinServerWithConfig(t, 2, JoinConfig{
		Enabled: true, BoothCode: "secret", ISD: 1,
		JoinableASes:         []int{152, 155},
		BootstrapURLTemplate: "http://10.20.3.%d:8041",
		EndpointV6:           "fd99::201", EndpointV4: "203.0.113.7", ListenPort: 51820,
		RateMax: 100, RateWindow: time.Minute,
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/join/meta", nil)
	req.SetBasicAuth("scion", "secret")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Joinable []struct {
			AS           int    `json:"as"`
			IsdAs        string `json:"isd_as"`
			BundleURL    string `json:"bundle_url"`
			BootstrapURL string `json:"bootstrap_url"`
		} `json:"joinable"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Joinable) != 2 {
		t.Fatalf("joinable len = %d, want 2", len(body.Joinable))
	}
	if body.Joinable[0].AS != 152 || body.Joinable[0].IsdAs != "1-152" ||
		body.Joinable[0].BundleURL != "/api/join/bundle/152" ||
		body.Joinable[0].BootstrapURL != "http://10.20.3.152:8041" {
		t.Fatalf("joinable[0] = %+v", body.Joinable[0])
	}
	if body.Joinable[1].AS != 155 || body.Joinable[1].IsdAs != "1-155" ||
		body.Joinable[1].BundleURL != "/api/join/bundle/155" ||
		body.Joinable[1].BootstrapURL != "http://10.20.3.155:8041" {
		t.Fatalf("joinable[1] = %+v", body.Joinable[1])
	}
}

func TestMetaBootstrapURLOmittedWhenTemplateEmpty(t *testing.T) {
	h := newJoinServerWithConfig(t, 2, JoinConfig{
		Enabled: true, BoothCode: "secret", ISD: 1,
		JoinableASes: []int{152, 155},
		EndpointV6:   "fd99::201", EndpointV4: "203.0.113.7", ListenPort: 51820,
		RateMax: 100, RateWindow: time.Minute,
		// BootstrapURLTemplate left empty.
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/join/meta", nil)
	req.SetBasicAuth("scion", "secret")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Joinable []struct {
			BootstrapURL string `json:"bootstrap_url"`
		} `json:"joinable"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Joinable) != 2 {
		t.Fatalf("joinable len = %d, want 2", len(body.Joinable))
	}
	for i, j := range body.Joinable {
		if j.BootstrapURL != "" {
			t.Fatalf("joinable[%d].bootstrap_url = %q, want empty", i, j.BootstrapURL)
		}
	}
}

func TestClaimWithoutASDefaultsToFirstJoinable(t *testing.T) {
	h := newJoinServerWithConfig(t, 2, JoinConfig{
		Enabled: true, BoothCode: "secret", ISD: 1,
		JoinableASes:         []int{152, 155, 158, 161},
		BootstrapURLTemplate: "http://10.20.3.%d:8041",
		EndpointV6:           "fd99::201", EndpointV4: "203.0.113.7", ListenPort: 51820,
		RateMax: 100, RateWindow: time.Minute,
	})
	rr := postClaim(t, h, `{"code":"secret"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["as"] != float64(152) {
		t.Fatalf("as = %v, want 152", resp["as"])
	}
	ids, ok := resp["fc00_identities"].(map[string]any)
	if !ok {
		t.Fatalf("fc00_identities missing or wrong shape: %v", resp["fc00_identities"])
	}
	for _, as := range []int{152, 155, 158, 161} {
		key := strconv.Itoa(as)
		if v, ok := ids[key]; !ok || v == "" {
			t.Fatalf("fc00_identities[%q] = %v, want non-empty", key, v)
		}
	}
}
