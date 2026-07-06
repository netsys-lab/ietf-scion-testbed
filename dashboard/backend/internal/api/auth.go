package api

import (
	"crypto/subtle"
	"net/http"
)

// requireBoothCode gates every route behind HTTP basic auth when a booth
// code is configured. Username and realm deliberately mirror the playground
// ttyd servers (--credential scion:<booth_code>, realm "ttyd"): the /play
// terminals are same-origin behind our reverse proxy, so a browser that has
// authenticated once silently satisfies both the dashboard and ttyd — one
// prompt for everything. GET /api/health stays open so runbook/monitoring
// curls need no credentials. With no booth code (dev, mock) the middleware
// is a no-op.
func (s *server) requireBoothCode(next http.Handler) http.Handler {
	code := s.join.BoothCode
	if code == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte("scion")) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(code)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="ttyd"`)
			http.Error(w, "booth code required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
