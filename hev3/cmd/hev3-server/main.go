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
	"net/netip"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
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
	scionIPFlag := fs.String("scion-ip", "", "SCION underlay bind IP for -scion (default: auto via sciond)")
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

	// SCION (when enabled) is set up first: its bound underlay IP is the one
	// address the ip-h3 listener(s) below must avoid, since dispatcher-less
	// SCION pins the underlay UDP port to the fixed SCION port (the zone's
	// SVCB port=443 applies to the SCION leg) and a wildcard ip-h3 bind on
	// that same port would collide with it.
	var servers []*server
	var scionIP netip.Addr
	if *useSCION {
		sc, err := newSCIONServer(context.Background(), withTransport(transportSCION, handler), cert, resolveDaemon(*daemonAddr), *scionIPFlag, *scionPort)
		if err != nil {
			fmt.Fprintf(stderr, "hev3-server: setting up SCION listener: %v\n", err)
			return 1
		}
		scionIP = sc.LocalIP()
		fmt.Fprintf(stderr, "hev3-server: scion-h3 listening on underlay udp %s port %d\n", scionIP, *scionPort)
		servers = append(servers, &server{name: "scion-h3", serve: sc.serve, shutdown: sc.shutdown})
	}

	tcpSrv, err := newTCPServer(*listenIP, cert, handler)
	if err != nil {
		fmt.Fprintf(stderr, "hev3-server: setting up ip-tcp listener: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "hev3-server: %s listening\n", tcpSrv.name)
	servers = append(servers, tcpSrv)

	h3Srv, detail, err := newH3IPServers(*listenIP, cert, handler, scionIP)
	if err != nil {
		fmt.Fprintf(stderr, "hev3-server: setting up ip-h3 listener(s): %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "hev3-server: %s listening%s\n", h3Srv.name, detail)
	servers = append(servers, h3Srv)

	return serveAll(servers, stderr)
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

// serveAll runs the accept/serve loop for every listener (each already
// synchronously bound by its constructor, and logged by the caller at that
// point — not here, since that log must be truthful about the bind, and the
// serve loop below can still fail asynchronously for unrelated reasons).
// It waits for SIGINT/SIGTERM or a fatal serve error, then shuts every
// listener down gracefully. Returns 0 on a clean signalled shutdown, 1 if a
// listener failed to serve.
func serveAll(servers []*server, stderr *os.File) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, len(servers))
	for _, s := range servers {
		s := s
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
