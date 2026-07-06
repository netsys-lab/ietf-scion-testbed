package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// authWrap builds the middleware around a trivial 200 handler, bypassing New:
// the middleware only reads s.join.BoothCode, so a struct literal suffices.
func authWrap(code string) http.Handler {
	s := &server{join: JoinConfig{BoothCode: code}}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return s.requireBoothCode(next)
}

func do(h http.Handler, method, path string, auth func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if auth != nil {
		auth(req)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestAuthOffWhenNoCode(t *testing.T) {
	rr := do(authWrap(""), http.MethodGet, "/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 with empty booth code, got %d", rr.Code)
	}
}

func TestAuth401WithoutCreds(t *testing.T) {
	rr := do(authWrap("ietf126"), http.MethodGet, "/", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
	if got := rr.Header().Get("WWW-Authenticate"); got != `Basic realm="ttyd"` {
		t.Fatalf("wrong WWW-Authenticate: %q", got)
	}
}

func TestAuth401WrongUser(t *testing.T) {
	rr := do(authWrap("ietf126"), http.MethodGet, "/api/topology",
		func(r *http.Request) { r.SetBasicAuth("admin", "ietf126") })
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

func TestAuth401WrongPass(t *testing.T) {
	rr := do(authWrap("ietf126"), http.MethodGet, "/api/topology",
		func(r *http.Request) { r.SetBasicAuth("scion", "wrong") })
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rr.Code)
	}
}

func TestAuth200WithCreds(t *testing.T) {
	rr := do(authWrap("ietf126"), http.MethodGet, "/",
		func(r *http.Request) { r.SetBasicAuth("scion", "ietf126") })
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
}

func TestHealthExemptGET(t *testing.T) {
	rr := do(authWrap("ietf126"), http.MethodGet, "/api/health", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200 on credential-free GET /api/health, got %d", rr.Code)
	}
}

func TestHealthGatedForOtherMethods(t *testing.T) {
	rr := do(authWrap("ietf126"), http.MethodPost, "/api/health", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 on POST /api/health, got %d", rr.Code)
	}
}
