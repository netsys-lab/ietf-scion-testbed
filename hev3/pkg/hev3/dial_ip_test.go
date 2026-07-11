package hev3

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
)

func TestDialIP_H2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hello-h2")
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	tcp := srv.Listener.Addr().(*net.TCPAddr)
	dial := NewDialer(DialerOptions{TLS: &tls.Config{InsecureSkipVerify: true}})
	c := Candidate{
		Family: FamilyIPv4,
		Host:   tcp.IP.String(),
		Port:   uint16(tcp.Port),
		ALPN:   []string{"h2"},
		Label:  "v4+h2",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	est, err := dial(ctx, c)
	if err != nil {
		t.Fatalf("dial h2: %v", err)
	}
	defer est.Close()

	if est.ALPN != "h2" {
		t.Fatalf("negotiated ALPN = %q, want h2", est.ALPN)
	}
	body, proto := roundTrip(t, est, c)
	if body != "hello-h2" {
		t.Fatalf("body = %q, want hello-h2", body)
	}
	if proto != "HTTP/2.0" {
		t.Fatalf("response proto = %q, want HTTP/2.0", proto)
	}
}

func TestDialIP_H3(t *testing.T) {
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("udp listen: %v", err)
	}
	srv := &http3.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, "hello-h3")
		}),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{selfSigned(t)},
			NextProtos:   []string{"h3"},
		},
	}
	go func() { _ = srv.Serve(udp) }()
	defer func() { _ = srv.Close() }()

	port := udp.LocalAddr().(*net.UDPAddr).Port
	dial := NewDialer(DialerOptions{TLS: &tls.Config{InsecureSkipVerify: true}})
	c := Candidate{
		Family: FamilyIPv4,
		Host:   "127.0.0.1",
		Port:   uint16(port),
		ALPN:   []string{"h3"},
		Label:  "v4+h3",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	est, err := dial(ctx, c)
	if err != nil {
		t.Fatalf("dial h3: %v", err)
	}
	defer est.Close()

	if est.ALPN != "h3" {
		t.Fatalf("negotiated ALPN = %q, want h3", est.ALPN)
	}
	body, proto := roundTrip(t, est, c)
	if body != "hello-h3" {
		t.Fatalf("body = %q, want hello-h3", body)
	}
	if proto != "HTTP/3.0" {
		t.Fatalf("response proto = %q, want HTTP/3.0", proto)
	}
}

// roundTrip issues one GET over est.RT and returns the body and response proto.
func roundTrip(t *testing.T, est *Established, c Candidate) (string, string) {
	t.Helper()
	url := fmt.Sprintf("https://%s/", net.JoinHostPort(c.Host, fmt.Sprint(c.Port)))
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := est.RT.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body), resp.Proto
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "hev3-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
