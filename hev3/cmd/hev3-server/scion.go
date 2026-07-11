package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/scionproto/scion/pkg/daemon"
	"github.com/scionproto/scion/pkg/snet"
	"github.com/scionproto/scion/pkg/snet/addrutil"
)

// scionServer serves HTTP/3 over native SCION QUIC. It mirrors the client-side
// dial in pkg/hev3/dial_scion.go: connect to the local sciond for topology and
// a local underlay IP, open an snet PacketConn, wrap it in a quic.Transport, and
// hand its QUIC listener to an http3.Server. The request's remote address is the
// snet UDPAddr string, so the peer's ISD-AS shows in the "reached over" payload.
type scionServer struct {
	h3      *http3.Server
	ln      *quic.Listener
	tr      *quic.Transport
	conn    *snet.Conn
	sciond  daemon.Connector
	localIP netip.Addr // underlay IP the SCION listener is bound to; see localAddr
}

// LocalIP is the underlay IP the SCION listener bound (dispatcher-less SCION:
// the SCION port must stay fixed per the zone's SVCB record, so the ip-h3
// listener(s) must avoid this address on that same port; see ipH3Addrs).
func (s *scionServer) LocalIP() netip.Addr { return s.localIP }

// localAddr extracts the bound underlay IP from an snet.Conn's LocalAddr, i.e.
// the concrete address newSCIONServer actually bound the SCION socket to
// (rather than re-deriving it from the pre-bind addrutil.DefaultLocalIP
// lookup, which is a hint, not a guarantee).
func localAddr(conn *snet.Conn) (netip.Addr, error) {
	ua, ok := conn.LocalAddr().(*snet.UDPAddr)
	if !ok || ua.Host == nil {
		return netip.Addr{}, fmt.Errorf("unexpected SCION local addr type %T", conn.LocalAddr())
	}
	ip, ok := netip.AddrFromSlice(ua.Host.IP)
	if !ok {
		return netip.Addr{}, fmt.Errorf("unexpected SCION local IP %v", ua.Host.IP)
	}
	return ip.Unmap(), nil
}

// newSCIONServer sets up the SCION QUIC HTTP/3 listener on the given underlay
// port using the local sciond at daemonAddr.
func newSCIONServer(ctx context.Context, handler http.Handler, cert tls.Certificate, daemonAddr string, port int) (*scionServer, error) {
	sciond, err := daemon.NewService(daemonAddr).Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon %s: %w", daemonAddr, err)
	}
	topo, err := daemon.LoadTopology(ctx, sciond)
	if err != nil {
		_ = sciond.Close()
		return nil, fmt.Errorf("loading topology: %w", err)
	}
	localIP, err := addrutil.DefaultLocalIP(ctx, daemon.TopoQuerier{Connector: sciond})
	if err != nil {
		_ = sciond.Close()
		return nil, fmt.Errorf("resolving local IP: %w", err)
	}

	sn := &snet.SCIONNetwork{
		Topology: topo,
		SCMPHandler: snet.SCMPPropagationStopper{
			Handler: snet.DefaultSCMPHandler{
				RevocationHandler: daemon.RevHandler{Connector: sciond},
			},
		},
	}
	conn, err := sn.Listen(ctx, "udp", &net.UDPAddr{IP: localIP, Port: port})
	if err != nil {
		_ = sciond.Close()
		return nil, fmt.Errorf("opening SCION socket: %w", err)
	}
	boundIP, err := localAddr(conn)
	if err != nil {
		_ = conn.Close()
		_ = sciond.Close()
		return nil, fmt.Errorf("resolving bound local addr: %w", err)
	}

	tr := &quic.Transport{Conn: conn}
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h3"},
	}
	ln, err := tr.Listen(tlsConf, &quic.Config{})
	if err != nil {
		_ = tr.Close()
		_ = conn.Close()
		_ = sciond.Close()
		return nil, fmt.Errorf("opening QUIC listener: %w", err)
	}

	h3 := &http3.Server{Handler: handler}
	return &scionServer{h3: h3, ln: ln, tr: tr, conn: conn, sciond: sciond, localIP: boundIP}, nil
}

func (s *scionServer) serve() error { return s.h3.ServeListener(s.ln) }

func (s *scionServer) shutdown(ctx context.Context) error {
	err := s.h3.Shutdown(ctx)
	_ = s.ln.Close()
	_ = s.tr.Close()
	_ = s.conn.Close()
	_ = s.sciond.Close()
	return err
}
