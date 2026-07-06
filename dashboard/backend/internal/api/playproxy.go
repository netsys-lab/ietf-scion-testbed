package api

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
)

// handlePlayRoot redirects /play/{as} to /play/{as}/ so ttyd's relative
// assets resolve under the proxy prefix.
func (s *server) handlePlayRoot(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.playTarget(r); !ok {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
}

func (s *server) playTarget(r *http.Request) (string, bool) {
	asNum, err := strconv.Atoi(r.PathValue("as"))
	if err != nil {
		return "", false
	}
	t, ok := s.join.PlayTargets[asNum]
	return t, ok
}

// handlePlayProxy reverse-proxies /play/{as}/<rest> to the playground ttyd,
// stripping the prefix. httputil.ReverseProxy passes WebSocket upgrades
// through (hijacks the connection) since Go 1.12 — ttyd terminals work.
// ttyd does its own basic-auth; the Authorization header rides along.
func (s *server) handlePlayProxy(w http.ResponseWriter, r *http.Request) {
	target, ok := s.playTarget(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	rest := r.PathValue("path")
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: target})
			pr.Out.URL.Path = "/" + rest
			pr.Out.Host = target
		},
	}
	rp.ServeHTTP(w, r)
}
