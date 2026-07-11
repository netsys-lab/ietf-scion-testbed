package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"

	"github.com/quic-go/quic-go/http3"
)

// ipH3Addrs computes the host:port addresses the ip-h3 (IP HTTP/3) listener
// should bind.
//
// Without SCION (scionIP invalid), or when explicitHost names a specific,
// non-wildcard address, there is exactly one address: honor -listen-ip as
// given (explicit wins, even in SCION mode).
//
// Otherwise (wildcard/empty host, SCION enabled) a single wildcard UDP
// listener would collide with the SCION listener's underlay socket: SCION's
// port must stay fixed at the zone's SVCB port (not negotiable), and Linux
// refuses a wildcard and a specific-address UDP bind sharing one port unless
// both sides set SO_REUSEADDR — which the snet PacketConn does not. So
// instead we enumerate the host's addresses (loopback + global unicast,
// excluding the SCION underlay IP and link-local) and bind one listener per
// address on the -listen-ip port; the -listen-ip host part is ignored.
//
// addrs is the address enumerator (net.InterfaceAddrs in production);
// injectable so tests can supply synthetic interface lists.
func ipH3Addrs(explicitHost, port string, scionIP netip.Addr, addrs func() ([]net.Addr, error)) ([]string, error) {
	if isExplicitHost(explicitHost) {
		hostIP, err := netip.ParseAddr(explicitHost)
		if err != nil {
			return nil, fmt.Errorf("ip-h3: -listen-ip host %q: %w", explicitHost, err)
		}
		if scionIP.IsValid() && hostIP.Unmap() == scionIP.Unmap() {
			return nil, fmt.Errorf("ip-h3: -listen-ip host %s is the SCION underlay address; "+
				"ip-h3 and scion-h3 cannot share one UDP port on the same address", explicitHost)
		}
		return []string{net.JoinHostPort(explicitHost, port)}, nil
	}

	if !scionIP.IsValid() {
		// No SCION listener to avoid: a plain wildcard bind is fine.
		return []string{net.JoinHostPort(explicitHost, port)}, nil
	}

	raw, err := addrs()
	if err != nil {
		return nil, fmt.Errorf("ip-h3: enumerating host addresses: %w", err)
	}

	excludeIP := scionIP.Unmap()
	seen := make(map[netip.Addr]bool)
	var out []string
	for _, a := range raw {
		ipNet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(ipNet.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if ip == excludeIP || ip.IsLinkLocalUnicast() || seen[ip] {
			continue
		}
		if !ip.IsLoopback() && !ip.IsGlobalUnicast() {
			continue
		}
		seen[ip] = true
		out = append(out, net.JoinHostPort(ip.String(), port))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ip-h3: no host addresses left to bind after excluding SCION underlay %s", scionIP)
	}
	return out, nil
}

// isExplicitHost reports whether host names a specific address rather than
// "bind everywhere" (empty, or an explicit IPv4/IPv6 wildcard).
func isExplicitHost(host string) bool {
	return host != "" && host != "0.0.0.0" && host != "::"
}

// newH3IPServers builds the ip-h3 HTTP/3 listener(s) for -listen-ip. scionIP
// must be the zero netip.Addr when -scion is disabled (single, unchanged
// wildcard-or-flag listener); otherwise it is the SCION listener's bound
// underlay IP, and see ipH3Addrs for how that changes the bind set. Returns
// the built server plus a human-readable detail string describing what was
// bound, for the caller to log once the (synchronous) bind below has
// actually succeeded.
func newH3IPServers(listenIP string, cert tls.Certificate, handler http.Handler, scionIP netip.Addr) (*server, string, error) {
	host, port, err := net.SplitHostPort(listenIP)
	if err != nil {
		return nil, "", fmt.Errorf("ip-h3: -listen-ip %q: %w", listenIP, err)
	}

	addrs, err := ipH3Addrs(host, port, scionIP, net.InterfaceAddrs)
	if err != nil {
		return nil, "", err
	}

	srv, err := buildH3IPServer(addrs, cert, handler)
	if err != nil {
		return nil, "", err
	}

	detail := ""
	switch {
	case !scionIP.IsValid():
		// unchanged single-listener path; nothing extra to report.
	case isExplicitHost(host):
		detail = fmt.Sprintf(" on %s (explicit -listen-ip host honored in -scion mode)", addrs[0])
	default:
		detail = fmt.Sprintf(" on %s (-listen-ip host ignored in -scion mode, port %s; excluding SCION underlay %s)",
			strings.Join(addrs, ", "), port, scionIP)
	}
	return srv, detail, nil
}

// buildH3IPServer binds one UDP listener per address in addrs synchronously
// (so the caller can log only after every bind has actually succeeded) and
// serves them all from one shared *http3.Server, so a single Shutdown call
// closes every listener.
func buildH3IPServer(addrs []string, cert tls.Certificate, handler http.Handler) (*server, error) {
	srv := &http3.Server{
		Handler: withTransport(transportIPH3, handler),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h3"},
		},
	}

	conns := make([]net.PacketConn, 0, len(addrs))
	for _, a := range addrs {
		conn, err := net.ListenPacket("udp", a)
		if err != nil {
			for _, c := range conns {
				_ = c.Close()
			}
			return nil, fmt.Errorf("ip-h3: listen udp %s: %w", a, err)
		}
		conns = append(conns, conn)
	}

	return &server{
		name: "ip-h3",
		serve: func() error {
			var wg sync.WaitGroup
			errCh := make(chan error, len(conns))
			for _, c := range conns {
				wg.Add(1)
				go func(c net.PacketConn) {
					defer wg.Done()
					errCh <- srv.Serve(c)
				}(c)
			}
			wg.Wait()
			close(errCh)
			var firstErr error
			for err := range errCh {
				if err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		},
		shutdown: func(ctx context.Context) error { return srv.Shutdown(ctx) },
	}, nil
}

// newTCPServer builds the IP TCP+TLS listener serving h2 and http/1.1,
// binding synchronously so the caller can log only after a successful bind.
// Unaffected by -scion: a wildcard TCP bind never conflicts with the SCION
// listener's UDP socket.
func newTCPServer(addr string, cert tls.Certificate, handler http.Handler) (*server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ip-tcp: %w", err)
	}
	srv := &http.Server{
		Handler: withTransport(transportIP, handler),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2", "http/1.1"},
		},
	}
	return &server{
		name:     "ip-tcp",
		serve:    func() error { return srv.ServeTLS(ln, "", "") },
		shutdown: srv.Shutdown,
	}, nil
}
