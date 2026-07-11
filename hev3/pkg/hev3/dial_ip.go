package hev3

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"sync"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/net/http2"
)

const (
	// defaultK caps the number of ranked SCION paths ExpandSCION keeps.
	defaultK = 3
	// defaultDaemonAddr is the sciond address used when neither DialerOptions
	// nor $SCION_DAEMON_ADDRESS specifies one.
	defaultDaemonAddr = "127.0.0.1:30255"

	alpnH3     = "h3"
	alpnH2     = "h2"
	alpnHTTP11 = "http/1.1"
)

// DialerOptions configures a DialFunc built by NewDialer.
type DialerOptions struct {
	TLS        *tls.Config // base client config; cloned per attempt, ALPN/ServerName overridden
	DaemonAddr string      // sciond address; empty ⇒ $SCION_DAEMON_ADDRESS or defaultDaemonAddr
	K          int         // ranked SCION paths per proto-candidate; 0 ⇒ defaultK
	Timeline   *Timeline   // optional; records ExpandSCION drop/scitra notes
}

// NewDialer returns a DialFunc that dispatches on the Candidate: a ViaScitra
// candidate dials its mapped IPv6 address; a FamilySCION candidate (with a
// pinned path) dials native SCION QUIC; anything else dials over IP, choosing
// HTTP/3-QUIC when the ALPN offers "h3" and TCP+TLS (h2/http1.1) otherwise.
func NewDialer(o DialerOptions) DialFunc {
	return func(ctx context.Context, c Candidate) (*Established, error) {
		switch {
		case c.ViaScitra:
			return dialScitra(ctx, c, o)
		case c.Family == FamilySCION:
			return dialSCION(ctx, c, o)
		default:
			return dialIP(ctx, c, o)
		}
	}
}

// daemonAddress resolves the sciond address per DialerOptions precedence.
func daemonAddress(o DialerOptions) string {
	if o.DaemonAddr != "" {
		return o.DaemonAddr
	}
	if env := os.Getenv("SCION_DAEMON_ADDRESS"); env != "" {
		return env
	}
	return defaultDaemonAddr
}

// dialIP dials an IP candidate: HTTP/3 when "h3" is offered, else TCP+TLS.
func dialIP(ctx context.Context, c Candidate, o DialerOptions) (*Established, error) {
	if offersH3(c.ALPN) {
		return dialH3IP(ctx, c, o)
	}
	return dialTCP(ctx, c, o)
}

func offersH3(alpn []string) bool {
	for _, a := range alpn {
		if a == alpnH3 {
			return true
		}
	}
	return false
}

// dialH3IP opens an HTTP/3 connection to the candidate over a fresh UDP socket
// and returns a single-connection RoundTripper (http3.ClientConn).
func dialH3IP(ctx context.Context, c Candidate, o DialerOptions) (*Established, error) {
	host, err := netip.ParseAddr(c.Host)
	if err != nil {
		return nil, fmt.Errorf("hev3: dial h3: bad host %q: %w", c.Host, err)
	}
	udpConn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, fmt.Errorf("hev3: dial h3: udp socket: %w", err)
	}
	tr := &quic.Transport{Conn: udpConn}
	remote := net.UDPAddrFromAddrPort(netip.AddrPortFrom(host, c.Port))
	qconn, err := tr.Dial(ctx, remote, h3TLSConfig(o.TLS, host.String()), nil)
	if err != nil {
		_ = tr.Close()
		_ = udpConn.Close()
		return nil, fmt.Errorf("hev3: dial h3 %s: %w", remote, err)
	}
	return newH3Established(c, qconn, func() error {
		err := tr.Close()
		_ = udpConn.Close()
		return err
	}), nil
}

// dialTCP opens a TLS-over-TCP connection and returns an h2 or http/1.1
// RoundTripper depending on the negotiated ALPN.
func dialTCP(ctx context.Context, c Candidate, o DialerOptions) (*Established, error) {
	address := net.JoinHostPort(c.Host, strconv.Itoa(int(c.Port)))
	d := &tls.Dialer{Config: tcpTLSConfig(o.TLS, c)}
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("hev3: dial tcp %s: %w", address, err)
	}
	tconn := conn.(*tls.Conn)
	proto := tconn.ConnectionState().NegotiatedProtocol

	if proto == alpnH2 {
		cc, err := (&http2.Transport{}).NewClientConn(tconn)
		if err != nil {
			_ = tconn.Close()
			return nil, fmt.Errorf("hev3: dial tcp %s: h2 client conn: %w", address, err)
		}
		return &Established{
			Cand:  c,
			RT:    cc,
			ALPN:  alpnH2,
			Close: tconn.Close,
		}, nil
	}
	// HTTP/1.1 (or a server that negotiated nothing) over the one TLS conn.
	return &Established{
		Cand:  c,
		RT:    oneConnH1Transport(tconn),
		ALPN:  orDefault(proto, alpnHTTP11),
		Close: tconn.Close,
	}, nil
}

// newH3Established wraps a dialed *quic.Conn in an http3.ClientConn RoundTripper.
// closeUnderlay releases the transport/socket backing the connection.
func newH3Established(c Candidate, qconn *quic.Conn, closeUnderlay func() error) *Established {
	h3tr := &http3.Transport{}
	cc := h3tr.NewClientConn(qconn)
	return &Established{
		Cand: c,
		RT:   cc,
		ALPN: negotiatedProto(qconn, alpnH3),
		Close: func() error {
			_ = cc.CloseWithError(0, "")
			_ = qconn.CloseWithError(0, "")
			_ = h3tr.Close()
			if closeUnderlay != nil {
				return closeUnderlay()
			}
			return nil
		},
	}
}

func negotiatedProto(qconn *quic.Conn, fallback string) string {
	if p := qconn.ConnectionState().TLS.NegotiatedProtocol; p != "" {
		return p
	}
	return fallback
}

// h3TLSConfig clones base (or starts empty), pins ALPN to h3, and defaults the
// SNI to serverName when unset.
func h3TLSConfig(base *tls.Config, serverName string) *tls.Config {
	c := cloneTLS(base)
	c.NextProtos = []string{alpnH3}
	if c.ServerName == "" {
		c.ServerName = serverName
	}
	return c
}

// tcpTLSConfig clones base and sets ALPN from the candidate (dropping h3, which
// is UDP-only), defaulting to h2 then http/1.1.
func tcpTLSConfig(base *tls.Config, c Candidate) *tls.Config {
	t := cloneTLS(base)
	var protos []string
	for _, a := range c.ALPN {
		if a != alpnH3 {
			protos = append(protos, a)
		}
	}
	if len(protos) == 0 {
		protos = []string{alpnH2, alpnHTTP11}
	}
	t.NextProtos = protos
	if t.ServerName == "" {
		t.ServerName = c.Host
	}
	return t
}

func cloneTLS(base *tls.Config) *tls.Config {
	if base != nil {
		return base.Clone()
	}
	return &tls.Config{}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// oneConnH1Transport serves req/response over a single already-established TLS
// connection. http.Transport reuses the conn for sequential keep-alive requests;
// a demand for a second concurrent conn (never expected for one Established
// winner) errors rather than dialing anew.
func oneConnH1Transport(conn net.Conn) http.RoundTripper {
	var (
		mu   sync.Mutex
		used bool
	)
	return &http.Transport{
		DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
			mu.Lock()
			defer mu.Unlock()
			if used {
				return nil, fmt.Errorf("hev3: h1 single connection already in use")
			}
			used = true
			return conn, nil
		},
	}
}
