package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
)

func TestEndhostSDTomlGolden(t *testing.T) {
	want := `[general]
id = "sd1-158"
config_dir = "."

[trust_db]
connection = "sd1-158.trust.db"

[path_db]
connection = "sd1-158.path.db"

[sd]
address = "127.0.0.1:30255"

[features]
experimental_idint = true

[drkey_level2_db]
connection = "sd1-158.drkey_level2.db"

[log.console]
level = "info"
`
	if got := endhostSDToml(158); got != want {
		t.Fatalf("endhost sd.toml mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func bundleConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	as := filepath.Join(dir, "AS158")
	if err := os.MkdirAll(filepath.Join(as, "certs"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(as, "topology.json"), []byte(`{"isd_as":"1-158"}`), 0o644)
	os.WriteFile(filepath.Join(as, "certs", "ISD1-B1-S1.trc"), []byte("TRC-BYTES"), 0o644)
	return dir
}

func newBundleServer(t *testing.T, configDir string) http.Handler {
	g := loadGraph(t)
	st := store.New(60)
	d := derive.New(g, st)
	jc := JoinConfig{Enabled: true, BoothCode: "x", JoinableASes: []int{158, 159, 160, 161}, ConfigDir: configDir}
	return New(g, st, d, &fakeController{health: map[int]bool{}, shaping: map[string]*derive.Shaping{}}, nil, jc, nil)
}

func readTarGz(t *testing.T, body []byte) map[string]string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		data, _ := io.ReadAll(tr)
		out[hdr.Name] = string(data)
	}
	return out
}

func TestBundleContents(t *testing.T) {
	h := newBundleServer(t, bundleConfigDir(t))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/join/bundle/158", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("content-type = %q", ct)
	}
	files := readTarGz(t, rr.Body.Bytes())
	if files["topology.json"] != `{"isd_as":"1-158"}` || files["certs/ISD1-B1-S1.trc"] != "TRC-BYTES" {
		t.Fatalf("bundle contents wrong: %v", files)
	}
	if files["sd.toml"] != endhostSDToml(158) {
		t.Fatal("sd.toml not the generated endhost config")
	}
	if _, ok := files["README.txt"]; !ok {
		t.Fatal("missing README.txt")
	}
}

func TestBundleNonJoinable404(t *testing.T) {
	h := newBundleServer(t, bundleConfigDir(t))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/join/bundle/150", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

func TestBundleBadAS400(t *testing.T) {
	h := newBundleServer(t, bundleConfigDir(t))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/join/bundle/abc", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}
