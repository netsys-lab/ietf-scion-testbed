// Package scitramap derives SCION-mapped IPv6 addresses (scitra's fc00::/8
// scheme): fc(8) | ISD(12) | ASN(20) | local(24)=0 | interfaceID(64), with
// IPv4 hosts embedded as 0x0000ffff:<v4> in the interface ID. These addresses
// never traverse the network as IPv6 — they are the identities IPv6 apps use
// through scitra-tun.
package scitramap

import (
	"fmt"
	"net/netip"
)

func MappedIPv6(isd, asn int, v4 netip.Addr) (netip.Addr, error) {
	if isd < 0 || isd >= 1<<12 {
		return netip.Addr{}, fmt.Errorf("scitramap: ISD %d exceeds 12 bits", isd)
	}
	if asn < 0 || asn >= 1<<19 {
		return netip.Addr{}, fmt.Errorf("scitramap: ASN %d exceeds direct-mapped range (2^19)", asn)
	}
	if !v4.Is4() {
		return netip.Addr{}, fmt.Errorf("scitramap: host %s is not IPv4", v4)
	}
	var b [16]byte
	b[0] = 0xfc
	b[1] = byte(isd >> 4)
	b[2] = byte(isd&0xf)<<4 | byte(asn>>16)
	b[3] = byte(asn >> 8)
	b[4] = byte(asn)
	// b[5..7] local prefix + subnet = 0; b[8..9] = 0, b[10..11] = 0xffff
	b[10], b[11] = 0xff, 0xff
	v4b := v4.As4()
	copy(b[12:], v4b[:])
	return netip.AddrFrom16(b), nil
}
