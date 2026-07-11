package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// transportCtxKey is the context key under which each listener stashes its base
// transport tag; the request handler reads it back via transportTag.
type transportCtxKey struct{}

// Base transport tags injected per-listener. transportIP is a placeholder that
// transportTag refines into ip-h2 / ip-http/1.1 from the negotiated TLS ALPN,
// since one TCP listener serves both.
const (
	transportSCION = "scion"
	transportIPH3  = "ip-h3"
	transportIP    = "ip"
)

// withTransport tags every request handled by h with the given base transport,
// stored in the request context for transportTag to read. main wraps each
// listener's handler with the matching base ("scion", "ip-h3", "ip"); tests use
// it to exercise tag injection directly.
func withTransport(base string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), transportCtxKey{}, base)
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

// transportTag resolves the concrete transport label for a request. The base
// tag comes from the request context (default "ip" when unset, e.g. in a bare
// httptest handler); an "ip" base is refined into ip-h2 / ip-http/1.1 from the
// negotiated TLS ALPN.
func transportTag(r *http.Request) string {
	base, _ := r.Context().Value(transportCtxKey{}).(string)
	if base == "" {
		base = transportIP
	}
	if base != transportIP {
		return base
	}
	proto := ""
	if r.TLS != nil {
		proto = r.TLS.NegotiatedProtocol
	}
	switch proto {
	case "h2":
		return "ip-h2"
	case "http/1.1", "":
		return "ip-http/1.1"
	default:
		return "ip-" + proto
	}
}

// whoamiJSON is the /whoami response body: the same facts as GET / plus the
// request path, so a client scripting against two demo hosts can tell which
// transport and host answered.
type whoamiJSON struct {
	Transport string `json:"transport"`
	Remote    string `json:"remote"`
	Server    string `json:"server"`
	Path      string `json:"path,omitempty"`
}

// newHandler builds the mux shared by every listener. hostname is echoed as the
// "server" field. GET / returns a plain-text summary; GET /whoami returns the
// same as JSON. The transport label is derived per-request from the injected
// context tag (and, for the IP TCP listener, the negotiated ALPN).
func newHandler(hostname string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whoamiJSON{
			Transport: transportTag(r),
			Remote:    r.RemoteAddr,
			Server:    hostname,
			Path:      r.URL.Path,
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "reached over %s\nremote: %s\nserver: %s\n",
			transportTag(r), r.RemoteAddr, hostname)
	})
	return mux
}
