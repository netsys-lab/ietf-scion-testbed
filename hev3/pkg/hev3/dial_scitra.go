package hev3

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"strings"

	"github.com/scionproto/scion/pkg/addr"
)

// scitraPrefixDefault is the default SCION-IP-translator /8 prefix byte
// (fc00::/8) used when a caller does not supply one. It matches the scitra
// CoreDNS plugin's default Prefix.
const scitraPrefixDefault byte = 0xfc

// procNetIPv6Route is the kernel IPv6 routing table. It is a var so tests can
// point scitraAvailable at a fixture instead of the live table.
var procNetIPv6Route = "/proc/net/ipv6_route"

// ScitraMap encodes a SCION address (IA + host IP) into its SCION-IP-translator
// mapped IPv6 address, bit-for-bit identical to the scitra CoreDNS plugin's
// scion2ip (github.com/tjohn327/coredns/plugin/scitra). Layout of the 16 bytes:
//
//	[0]      prefix (fc00::/8 by default)
//	[1:5]    uint32 (ISD<<20)|asnEnc, big-endian, where asnEnc is the raw ASN
//	         when ASN < 2^19, or (1<<19)|(ASN&0x7ffff) for the BGP hex range
//	         0x2_0000_0000..0x2_0007_ffff; any other ASN is unmappable
//	[5:8]    local prefix / subnet ID, always zero here (plugin TODO defaults)
//	[8:16]   host: IPv4 as 0x0000ffff‖v4, IPv6 as the low 8 bytes of the v6
func ScitraMap(ia string, host netip.Addr, prefix byte) (netip.Addr, error) {
	parsed, err := addr.ParseIA(ia)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("hev3: scitra: parsing IA %q: %w", ia, err)
	}
	ip := host.Unmap()

	var b [16]byte
	b[0] = prefix

	isd := uint32(parsed.ISD())
	if isd >= (1 << 12) {
		return netip.Addr{}, fmt.Errorf("hev3: scitra: ISD %d cannot be encoded", isd)
	}
	asn := uint64(parsed.AS())
	var enc uint32
	switch {
	case asn < (1 << 19):
		enc = (isd << 20) | uint32(asn)
	case asn >= 0x2_0000_0000 && asn <= 0x2_0007_ffff:
		enc = (isd << 20) | (1 << 19) | (uint32(asn) & 0x7ffff)
	default:
		return netip.Addr{}, fmt.Errorf("hev3: scitra: ASN %d cannot be encoded", asn)
	}
	binary.BigEndian.PutUint32(b[1:5], enc)

	// b[5:8] (local prefix + subnet) stay zero, matching the plugin defaults.

	if ip.Is4() {
		binary.BigEndian.PutUint32(b[8:12], 0xffff)
		v4 := ip.As4()
		copy(b[12:16], v4[:])
	} else {
		v6 := ip.As16()
		copy(b[8:16], v6[8:16])
	}
	return netip.AddrFrom16(b), nil
}

// scitraAvailable reports whether the host has a SCION-IP-translator route,
// i.e. an fc00::/8 entry (destination prefix byte 0xfc, prefix length 8) in the
// kernel IPv6 routing table. A missing/unreadable table reads as unavailable.
func scitraAvailable() bool {
	data, err := os.ReadFile(procNetIPv6Route)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Field 0 is the 32-hex-char destination network (no colons); field 1
		// is the destination prefix length in hex. fc00::/8 ⇒ "fc..." with "08".
		if fields[1] == "08" && strings.HasPrefix(fields[0], "fc") {
			return true
		}
	}
	return false
}

// dialScitra dials a scitra-mapped candidate. ExpandSCION has already rewritten
// Host to the mapped IPv6 literal and cleared the SCION path, so this is just an
// ordinary IP dial over the translator route.
func dialScitra(ctx context.Context, c Candidate, o DialerOptions) (*Established, error) {
	return dialIP(ctx, c, o)
}
