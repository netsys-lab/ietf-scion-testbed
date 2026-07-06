package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
)

func instructionsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "faq.md"), []byte("# Frequently asked\n\nstuff"), 0o644)
	os.WriteFile(filepath.Join(dir, "laptop-linux.md"), []byte("# Laptop — Linux\n\nsteps"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644)
	return dir
}

func newInstrServer(t *testing.T, dir string) http.Handler {
	g := loadGraph(t)
	st := store.New(60)
	d := derive.New(g, st)
	jc := JoinConfig{InstructionsDir: dir}
	return New(g, st, d, &fakeController{health: map[int]bool{}, shaping: map[string]*derive.Shaping{}}, nil, jc, nil)
}

func TestInstructionsList(t *testing.T) {
	h := newInstrServer(t, instructionsDir(t))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/instructions", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var list []map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 md files, got %d: %v", len(list), list)
	}
	byName := map[string]string{}
	for _, e := range list {
		byName[e["name"]] = e["title"]
	}
	if byName["laptop-linux.md"] != "Laptop — Linux" {
		t.Fatalf("title from heading wrong: %v", byName)
	}
}

func TestInstructionRaw(t *testing.T) {
	h := newInstrServer(t, instructionsDir(t))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/instructions/faq.md", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "# Frequently asked\n\nstuff" {
		t.Fatalf("got %d %q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestInstructionRejectsBadNames(t *testing.T) {
	h := newInstrServer(t, instructionsDir(t))
	for _, name := range []string{"..%2Fsecret.md", "evil.txt", "sub/dir.md"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/instructions/"+name, nil))
		if rr.Code == http.StatusOK {
			t.Fatalf("bad name %q must not 200", name)
		}
	}
}

func TestInstructionMissingIs404(t *testing.T) {
	h := newInstrServer(t, instructionsDir(t))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/instructions/nope.md", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}

func TestInstructionsUnsetDirIsEmpty(t *testing.T) {
	h := newInstrServer(t, "")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/instructions", nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "[]\n" {
		t.Fatalf("want [] for unset dir, got %d %q", rr.Code, rr.Body.String())
	}
}
