// Command hev3-server is the demo target that the hev3 CLI races toward. It
// serves the same tiny page over every transport hev3 can win on: HTTP/2 and
// HTTP/1.1 over TCP+TLS, HTTP/3 over IP QUIC, and — with -scion — native
// HTTP/3 over SCION QUIC. Each response is tagged with the transport it
// arrived on (and, for SCION, the peer's ISD-AS), so a demo can show which leg
// of the Happy-Eyeballs race actually delivered the request.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// server is one running listener: serve blocks until shutdown, shutdown stops
// it. Both http.Server and http3.Server fit this shape; scionServer adapts to it.
type server struct {
	name     string
	serve    func() error
	shutdown func(context.Context) error
}

func run(args []string, stderr *os.File) int {
	fs := flag.NewFlagSet("hev3-server", flag.ContinueOnError)
	fs.SetOutput(stderr)

	listenIP := fs.String("listen-ip", ":443", "host:port for the IP TCP (h2/http1.1) and UDP (h3) listeners")
	certFile := fs.String("cert", "/etc/hev3/cert.pem", "TLS certificate PEM")
	keyFile := fs.String("key", "/etc/hev3/key.pem", "TLS private key PEM")
	useSCION := fs.Bool("scion", false, "also serve native HTTP/3 over SCION QUIC")
	scionPort := fs.Int("scion-port", 443, "SCION/UDP underlay port for the -scion listener")
	daemonAddr := fs.String("daemon", "", "sciond address for -scion (default $SCION_DAEMON_ADDRESS or 127.0.0.1:30255)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		fmt.Fprintf(stderr, "hev3-server: loading keypair: %v\n", err)
		return 1
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "hev3-server"
	}
	handler := newHandler(hostname)

	servers := []*server{
		newTCPServer(*listenIP, cert, handler),
		newH3IPServer(*listenIP, cert, handler),
	}

	if *useSCION {
		sc, err := newSCIONServer(context.Background(), withTransport(transportSCION, handler), cert, resolveDaemon(*daemonAddr), *scionPort)
		if err != nil {
			fmt.Fprintf(stderr, "hev3-server: setting up SCION listener: %v\n", err)
			return 1
		}
		servers = append(servers, &server{name: "scion-h3", serve: sc.serve, shutdown: sc.shutdown})
	}

	return serveAll(servers, stderr)
}

// newTCPServer builds the IP TCP+TLS listener serving h2 and http/1.1. The
// request handler is tagged "ip"; transportTag refines it per-request from the
// negotiated ALPN.
func newTCPServer(addr string, cert tls.Certificate, handler http.Handler) *server {
	srv := &http.Server{
		Addr:    addr,
		Handler: withTransport(transportIP, handler),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2", "http/1.1"},
		},
	}
	return &server{
		name:     "ip-tcp",
		serve:    func() error { return srv.ListenAndServeTLS("", "") },
		shutdown: srv.Shutdown,
	}
}

// newH3IPServer builds the IP UDP HTTP/3 listener.
func newH3IPServer(addr string, cert tls.Certificate, handler http.Handler) *server {
	srv := &http3.Server{
		Addr:    addr,
		Handler: withTransport(transportIPH3, handler),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h3"},
		},
	}
	return &server{
		name:     "ip-h3",
		serve:    srv.ListenAndServe,
		shutdown: func(ctx context.Context) error { return srv.Shutdown(ctx) },
	}
}

func resolveDaemon(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if env := os.Getenv("SCION_DAEMON_ADDRESS"); env != "" {
		return env
	}
	return "127.0.0.1:30255"
}

// serveAll starts every listener, waits for SIGINT/SIGTERM or a fatal serve
// error, then shuts them all down gracefully. It returns 0 on a clean signalled
// shutdown, 1 if a listener failed to start/serve.
func serveAll(servers []*server, stderr *os.File) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, len(servers))
	for _, s := range servers {
		s := s
		fmt.Fprintf(stderr, "hev3-server: %s listening\n", s.name)
		go func() {
			err := s.serve()
			if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, quic.ErrServerClosed) {
				errCh <- fmt.Errorf("%s: %w", s.name, err)
				return
			}
			errCh <- nil
		}()
	}

	exit := 0
	select {
	case <-ctx.Done():
		fmt.Fprintln(stderr, "hev3-server: shutting down")
	case err := <-errCh:
		if err != nil {
			fmt.Fprintf(stderr, "hev3-server: %v\n", err)
			exit = 1
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.shutdown(shutdownCtx)
	}
	return exit
}
