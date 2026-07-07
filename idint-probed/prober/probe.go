package prober

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/scionproto/scion/pkg/addr"
	"github.com/scionproto/scion/pkg/daemon"
	daemon_types "github.com/scionproto/scion/pkg/daemon/types"
	"github.com/scionproto/scion/pkg/private/serrors"
	"github.com/scionproto/scion/pkg/slayers"
	"github.com/scionproto/scion/pkg/slayers/idint"
	"github.com/scionproto/scion/pkg/snet"
)

// Ported from github.com/netsys-lab/idint-traceroute (Apache-2.0):
// main.go connectToNetwork/SCMPHandler and client/client.go
// sendProbe/receiveResponse/decodeProbe, with a fixed probe configuration.

// Sentinel errors mapped to HTTP statuses by the server (404 / 504).
var (
	ErrFingerprintNotFound = errors.New("fingerprint not found")
	ErrTimeout             = errors.New("probe timeout")
)

// Engine is the probe backend consumed by the HTTP server.
type Engine interface {
	Paths(ctx context.Context, dst addr.IA) (*PathsResponse, error)
	Probe(ctx context.Context, remote *snet.UDPAddr, fingerprint string) (*ProbeResult, error)
}

// Network is a connection to the local SCION stack (sciond + topology).
type Network struct {
	Snet    snet.SCIONNetwork
	Sciond  daemon.Connector
	LocalIA addr.IA
	Local   netip.Addr // mgmt IP we bind probes to
}

var _ Engine = (*Network)(nil)

// Connect dials sciond and loads the local topology. local is the IP probes
// bind to (the host part of the HTTP listen address).
func Connect(ctx context.Context, sciondAddr string, local netip.Addr) (*Network, error) {
	sciond, err := daemon.NewService(sciondAddr).Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to daemon %s: %w", sciondAddr, err)
	}
	localIA, err := sciond.LocalIA(ctx)
	if err != nil {
		return nil, fmt.Errorf("SCION daemon communication failed: %w", err)
	}
	topo, err := daemon.LoadTopology(ctx, sciond)
	if err != nil {
		return nil, fmt.Errorf("loading topology failed: %w", err)
	}
	return &Network{
		Snet: snet.SCIONNetwork{
			Topology:    topo,
			SCMPHandler: scmpHandler{},
		},
		Sciond:  sciond,
		LocalIA: localIA,
		Local:   local,
	}, nil
}

// scmpHandler ignores SCMP info messages and logs error messages, like the
// reference tool's SCMPHandler. Returning nil makes snet keep reading.
type scmpHandler struct{}

func (h scmpHandler) Handle(pkt *snet.Packet) error {
	scmp, ok := pkt.Payload.(snet.SCMPPayload)
	if !ok {
		return serrors.New("scmp handler invoked with non-scmp packet", "pkt", pkt)
	}
	typeCode := slayers.CreateSCMPTypeCode(scmp.Type(), scmp.Code())
	if typeCode.InfoMsg() {
		return nil
	}
	switch scmp.Type() {
	case slayers.SCMPTypeDestinationUnreachable:
		log.Println("SCMP Destination Unreachable")
	case slayers.SCMPTypePacketTooBig:
		log.Println("SCMP Packet Too Big")
	case slayers.SCMPTypeParameterProblem:
		log.Println("SCMP Parameter Problem")
	case slayers.SCMPTypeExternalInterfaceDown:
		log.Println("SCMP External Interface Down")
	case slayers.SCMPTypeInternalConnectivityDown:
		log.Println("SCMP Internal Connectivity Down")
	default:
		log.Printf("SCMP type %v code %v", scmp.Type(), scmp.Code())
	}
	return nil
}

// Paths lists sciond's paths to dst in sciond order ([0] = current best).
func (n *Network) Paths(ctx context.Context, dst addr.IA) (*PathsResponse, error) {
	paths, err := n.Sciond.Paths(ctx, dst, n.LocalIA, daemon_types.PathReqFlags{})
	if err != nil {
		return nil, fmt.Errorf("sciond paths: %w", err)
	}
	resp := &PathsResponse{
		LocalIA: n.LocalIA.String(),
		Paths:   make([]PathJSON, 0, len(paths)),
	}
	for _, p := range paths {
		resp.Paths = append(resp.Paths, PathToJSON(p))
	}
	return resp, nil
}

// Probe sends one ID-INT probe to remote over the path identified by
// fingerprint ("" = sciond's first path) and decodes the forward and reverse
// telemetry reports. A missing fingerprint yields ErrFingerprintNotFound; a
// read-deadline expiry yields ErrTimeout.
func (n *Network) Probe(
	ctx context.Context,
	remote *snet.UDPAddr,
	fingerprint string,
) (res *ProbeResult, err error) {
	// The fork's path decode helpers panic on malformed/non-SCION paths.
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("probe panic: %v", r)
		}
	}()

	// 1. Enumerate paths and select by fingerprint.
	paths, err := n.Sciond.Paths(ctx, remote.IA, n.LocalIA, daemon_types.PathReqFlags{})
	if err != nil {
		return nil, fmt.Errorf("sciond paths: %w", err)
	}
	var via snet.Path
	if fingerprint == "" {
		if len(paths) == 0 {
			return nil, fmt.Errorf("no path available to %s", remote.IA)
		}
		via = paths[0]
	} else {
		for _, p := range paths {
			if snet.Fingerprint(p.Metadata().Interfaces).String() == fingerprint {
				via = p
				break
			}
		}
		if via == nil {
			return nil, ErrFingerprintNotFound
		}
	}

	// 2. Open a raw SCION socket on the local IP with an ephemeral port.
	conn, err := n.Snet.OpenRaw(ctx, &net.UDPAddr{IP: n.Local.AsSlice(), Port: 0})
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer conn.Close()
	localUDP, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return nil, fmt.Errorf("unexpected local address type %T", conn.LocalAddr())
	}
	localPort := uint16(localUDP.Port)

	// 3. Special case of the key derivation: generate a key for communication
	// with our future self (as in the tool's sendProbe).
	keyCache := snet.KeyCache{
		Sciond:  n.Sciond,
		DstIA:   n.LocalIA,
		DstHost: n.Local,
	}
	validity := time.Now()
	self := addr.Addr{
		IA:   n.LocalIA,
		Host: addr.HostIP(n.Local),
	}
	key, err := keyCache.GetHostHostKey(ctx, validity, self, self)
	if err != nil {
		return nil, fmt.Errorf("getting host-host key: %w", err)
	}

	// 4. Build the probe packet exactly like the tool's sendProbe, with the
	// fixed instruction set and request flags.
	maxStackLen := (int(via.Metadata().MTU) - 512) / 2
	maxStackLen = (maxStackLen + 3) & ^3 // round up to a multiple of 4

	pkt := &snet.Packet{
		Bytes: nil,
		PacketInfo: snet.PacketInfo{
			Source: snet.SCIONAddress{
				IA:   n.LocalIA,
				Host: addr.HostIP(n.Local),
			},
			Destination: snet.SCIONAddress{
				IA:   remote.IA,
				Host: addr.HostIP(remote.Host.AddrPort().Addr()),
			},
			Path: via.Dataplane(),
			Payload: snet.UDPPayload{
				SrcPort: localPort,
				DstPort: remote.Host.AddrPort().Port(),
				Payload: []byte("probe"),
			},
			Telemetry: snet.IdIntInfo{Request: &snet.IntRequest{
				Encrypt:         false,
				SkipHops:        0,
				MaxStackLen:     maxStackLen,
				ReqNodeId:       true,
				ReqNodeCount:    false,
				ReqIgPort:       true,
				ReqEgPort:       true,
				AggregationMode: idint.AgOff,
				AggregationFunc: [4]uint8{1, 1, 1, 1}, // "last"
				Instructions:    FixedInstructions,
				Verifier:        idint.VfSrc,
				SourceMetadata:  snet.IntMetadata{},
				SourceTS:        validity,
				SourceKey:       slayers.IdIntKey(key),
			}},
		},
	}

	// 5. Send, then wait up to 1s for the reflected response.
	start := time.Now()
	if err := conn.WriteTo(pkt, via.UnderlayNextHop()); err != nil {
		return nil, fmt.Errorf("sending probe failed: %w", err)
	}
	if err := conn.SetReadDeadline(start.Add(1 * time.Second)); err != nil {
		return nil, fmt.Errorf("setting read deadline: %w", err)
	}
	reply := &snet.Packet{}
	var ov net.UDPAddr
	if err := conn.ReadFrom(reply, &ov); err != nil {
		if isTimeout(err) {
			return nil, ErrTimeout
		}
		return nil, fmt.Errorf("receiving response: %w", err)
	}
	rttMs := float64(time.Since(start)) / float64(time.Millisecond)

	if reply.PacketInfo.Telemetry.Report == nil {
		return nil, errors.New("response does not contain ID-INT")
	}

	// 6. Decode forward report from the UDP payload and reverse report from
	// the ID-INT header, mirroring the tool's decodeProbe.
	udp, ok := reply.PacketInfo.Payload.(snet.UDPPayload)
	if !ok {
		return nil, errors.New("non-UDP packet received")
	}
	rawFwd := snet.RawIntReport{}
	if err := rawFwd.ParseFromSlice(udp.Payload); err != nil {
		return nil, fmt.Errorf("decoding probe payload: %w", err)
	}
	fwd := &snet.IntReport{}
	err = rawFwd.VerifyAndDecrypt(
		ctx, fwd, reply.PacketInfo.Destination, &keyCache, FwdPathMeta(via))
	if err != nil {
		return nil, fmt.Errorf("decoding forward path: %w", err)
	}

	rawRev := reply.PacketInfo.Telemetry.Report
	rev := &snet.IntReport{}
	err = rawRev.VerifyAndDecrypt(
		ctx, rev, reply.PacketInfo.Source, &keyCache, RevPathMeta(via))
	if err != nil {
		return nil, fmt.Errorf("decoding reverse path: %w", err)
	}

	// 7. Convert both directions to wire records.
	fwdRecs, err := Records(fwd, FwdPathMeta(via), true)
	if err != nil {
		return nil, fmt.Errorf("forward records: %w", err)
	}
	revRecs, err := Records(rev, RevPathMeta(via), true)
	if err != nil {
		return nil, fmt.Errorf("reverse records: %w", err)
	}

	return &ProbeResult{
		Path:           PathToJSON(via),
		ProbeRttMs:     rttMs,
		MaxLenExceeded: fwd.MaxLengthExceeded || rev.MaxLengthExceeded,
		Fwd:            fwdRecs,
		Rev:            revRecs,
	}, nil
}

// isTimeout reports whether err is a read-deadline expiry.
func isTimeout(err error) bool {
	if os.IsTimeout(err) {
		return true
	}
	var nerr net.Error
	return errors.As(err, &nerr) && nerr.Timeout()
}
