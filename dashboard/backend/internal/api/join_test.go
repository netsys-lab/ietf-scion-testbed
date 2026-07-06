package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
