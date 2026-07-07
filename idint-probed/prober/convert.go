package prober

import (
	"fmt"
	"time"

	"github.com/scionproto/scion/pkg/addr"
	"github.com/scionproto/scion/pkg/private/serrors"
	"github.com/scionproto/scion/pkg/slayers/idint"
	scion "github.com/scionproto/scion/pkg/slayers/path/scion"
	"github.com/scionproto/scion/pkg/snet"
	spath "github.com/scionproto/scion/pkg/snet/path"
)

// Ported from github.com/netsys-lab/idint-traceroute client/client.go (Apache-2.0).

func hopfieldToInterfaceIdx(path snet.Path, reverse bool) func(uint) uint {
	// Mapping the hop field index back to an interface in the SNET path
	// metadata requires knowing the number and length of path segments. The
	// reason is that that one holp field covers two interfaces, except at the
	// very first and last hop, and at the splicing point between two segments
	// where one hop field covers only a single interface as defined in the SNET
	// interface metadata.
	var d scion.Decoded
	rp, ok := path.Dataplane().(spath.SCION)
	if !ok {
		panic("can't deal with non-SCION paths")
	}
	if err := d.DecodeFromBytes(rp.Raw); err != nil {
		panic(err)
	}

	if reverse {
		// Reverse segment length and info fields
		// No need to do a full reverse, as we don't need the hop fields
		if d.PathMeta.SegLen[2] > 0 {
			d.PathMeta.SegLen[0], d.PathMeta.SegLen[2] = d.PathMeta.SegLen[2], d.PathMeta.SegLen[0]
			d.InfoFields[0], d.InfoFields[2] = d.InfoFields[2], d.InfoFields[0]
		} else if d.PathMeta.SegLen[1] > 0 {
			d.PathMeta.SegLen[0], d.PathMeta.SegLen[1] = d.PathMeta.SegLen[1], d.PathMeta.SegLen[0]
			d.InfoFields[0], d.InfoFields[1] = d.InfoFields[1], d.InfoFields[0]
		}
	}

	// Hop field indices at which a segment change occurs
	segChange := [3]uint{
		uint(d.PathMeta.SegLen[0]), uint(d.PathMeta.SegLen[1]), uint(d.PathMeta.SegLen[2]),
	}
	segChange[1] = segChange[0] + segChange[1]
	segChange[2] = segChange[1] + segChange[2]

	// Special case: Ignore peering crossover as those have one hop field less
	for i := range d.NumINF - 1 {
		if d.InfoFields[i].Peer {
			segChange[i] = segChange[i+1]
		}
	}

	return func(i uint) uint {
		if i > 0 {
			if i < segChange[0] {
				return 2*i - 1
			} else if i < segChange[1] {
				return 2*i - 3
			} else {
				return 2*i - 5
			}
		}
		return 0
	}
}

// FwdPathMeta maps a forward-direction hop field index to the traversed IA.
func FwdPathMeta(path snet.Path) snet.HopToIA {
	hfToIface := hopfieldToInterfaceIdx(path, false)
	return func(i uint) (addr.IA, error) {
		j := hfToIface(i)
		if j < uint(len(path.Metadata().Interfaces)) {
			return path.Metadata().Interfaces[j].IA, nil
		}
		return 0, serrors.New("hop index out of range")
	}
}

// RevPathMeta maps a reverse-direction hop field index to the traversed IA.
func RevPathMeta(path snet.Path) snet.HopToIA {
	hfToIface := hopfieldToInterfaceIdx(path, true)
	return func(i uint) (addr.IA, error) {
		j := hfToIface(i)
		length := uint(len(path.Metadata().Interfaces))
		if j < length {
			return path.Metadata().Interfaces[length-j-1].IA, nil
		}
		return 0, serrors.New("hop index out of range")
	}
}

// PathToJSON flattens a sciond path into the wire shape. Latency entries
// follow the metadata slice; unset (negative) becomes -1.
func PathToJSON(p snet.Path) PathJSON {
	md := p.Metadata()
	ifaces := make([]IfaceJSON, len(md.Interfaces))
	for i, in := range md.Interfaces {
		ifaces[i] = IfaceJSON{IA: in.IA.String(), IfID: uint64(in.ID)}
	}
	lat := make([]int64, len(md.Latency))
	for i, d := range md.Latency {
		if d < 0 {
			lat[i] = -1
		} else {
			lat[i] = d.Microseconds()
		}
	}
	return PathJSON{
		Fingerprint: snet.Fingerprint(md.Interfaces).String(),
		MTU:         int(md.MTU),
		Expiry:      md.Expiry.UTC().Format(time.RFC3339),
		Interfaces:  ifaces,
		LatencyUs:   lat,
	}
}

// Records converts one decoded IntReport into wire HopRecords. Slot values
// are matched by the report's Instructions array (not assumed positional),
// so a router that omitted a slot yields a nil pointer, not a zero.
func Records(report *snet.IntReport, hopToIA snet.HopToIA, verified bool) ([]HopRecord, error) {
	out := make([]HopRecord, 0, len(report.Data))
	for i := range report.Data {
		hop := &report.Data[i]
		ia, err := hopToIA(uint(hop.HopIndex))
		if err != nil {
			return nil, fmt.Errorf("hop %d: %w", hop.HopIndex, err)
		}
		r := HopRecord{
			Hop: int(hop.HopIndex), IA: ia.String(),
			Source: hop.Source, Ingress: hop.Ingress, Egress: hop.Egress,
			Aggregated: hop.Aggregated, Encrypted: hop.Encrypted, Verified: verified,
		}
		if hop.HasNodeId() {
			v := hop.NodeId
			r.NodeId = &v
		}
		if hop.HasIngressPort() {
			v := hop.IngressPort
			r.IgrIfid = &v
		}
		if hop.HasEgressPort() {
			v := hop.EgressPort
			r.EgrIfid = &v
		}
		for s := 0; s < 4; s++ {
			if hop.DataLength(s) == 0 {
				continue
			}
			val := hop.DataSlots[s]
			switch report.Instructions[s] {
			case idint.InRttNextBr:
				us := int64(val)
				r.RttNextBrUs = &us
			case idint.InEgressLinkTx:
				pct := 100.0 * float64(val) / float64(^uint32(0))
				r.EgrLinkTxPct = &pct
			case idint.InIngressTstamp:
				ts := val
				r.IngressTstamp = &ts
			case idint.InInstQueueLen:
				q := int64(val)
				r.QueueLen = &q
			}
		}
		out = append(out, r)
	}
	return out, nil
}
