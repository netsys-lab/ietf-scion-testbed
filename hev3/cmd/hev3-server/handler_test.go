package main

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// getBody issues a GET against handler h and returns the response and body.
func getBody(t *testing.T, h http.Handler, path string) (*http.Response, string) {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp, string(b)
}

func TestRootBodyTransportInjection(t *testing.T) {
	h := withTransport(transportSCION, newHandler("web.scion"))
	resp, body := getBody(t, h, "/")

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{"reached over scion\n", "remote: ", "server: web.scion\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q missing %q", body, want)
		}
	}
}

func TestRootDefaultsToIPHTTP11WhenUntagged(t *testing.T) {
	// A bare handler with no injected tag and no TLS reports ip-http/1.1.
	_, body := getBody(t, newHandler("web.scion"), "/")
	if !strings.Contains(body, "reached over ip-http/1.1\n") {
		t.Errorf("untagged body = %q, want ip-http/1.1", body)
	}
}

func TestWhoamiJSONShape(t *testing.T) {
	h := withTransport(transportIPH3, newHandler("web2.scion"))
	resp, body := getBody(t, h, "/whoami")

	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var got whoamiJSON
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	if got.Transport != transportIPH3 {
		t.Errorf("transport = %q, want %q", got.Transport, transportIPH3)
	}
	if got.Server != "web2.scion" {
		t.Errorf("server = %q, want web2.scion", got.Server)
	}
	if got.Remote == "" {
		t.Error("remote is empty")
	}
	if got.Path != "/whoami" {
		t.Errorf("path = %q, want /whoami", got.Path)
	}
}

// TestNegotiatedProtoReporting drives the handler over a real TLS server so
// r.TLS.NegotiatedProtocol is populated, and asserts the "ip" base is refined
// into ip-h2 vs ip-http/1.1 from the negotiated ALPN.
func TestNegotiatedProtoReporting(t *testing.T) {
	cases := []struct {
		name      string
		enableH2  bool
		wantProto string
		wantTag   string
	}{
		{"h2", true, "h2", "ip-h2"},
		{"http1.1", false, "http/1.1", "ip-http/1.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewUnstartedServer(withTransport(transportIP, newHandler("web.scion")))
			srv.EnableHTTP2 = tc.enableH2
			srv.StartTLS()
			defer srv.Close()

			client := srv.Client()
			// Force http/1.1 by clearing the ALPN the client offers.
			if !tc.enableH2 {
				tr := client.Transport.(*http.Transport)
				tr.ForceAttemptHTTP2 = false
				if tr.TLSClientConfig == nil {
					tr.TLSClientConfig = &tls.Config{}
				}
				tr.TLSClientConfig.NextProtos = []string{"http/1.1"}
			}

			resp, err := client.Get(srv.URL + "/whoami")
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.TLS == nil || resp.TLS.NegotiatedProtocol != tc.wantProto {
				t.Fatalf("negotiated proto = %v, want %q", resp.TLS, tc.wantProto)
			}
			var got whoamiJSON
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("decode %q: %v", b, err)
			}
			if got.Transport != tc.wantTag {
				t.Errorf("transport = %q, want %q", got.Transport, tc.wantTag)
			}
		})
	}
}
