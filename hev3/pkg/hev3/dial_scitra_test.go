package hev3

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func TestScitraMapGolden(t *testing.T) {
	// Golden vectors computed from the scitra plugin's scion2ip algorithm.
	// The 1-150,10.20.3.216 case is verified live against the vendored plugin
	// (Task 2): ISD 1<<20|150 = 0x00100096 ⇒ bytes fc 00 10 00 96; v4 host
	// 10.20.3.216 ⇒ 00 00 ff ff 0a 14 03 d8.
	tests := []struct {
		name   string
		ia     string
		host   string
		prefix byte
		want   string
	}{
		{
			name:   "bgp-asn v4 host (live-verified)",
			ia:     "1-150",
			host:   "10.20.3.216",
			prefix: 0xfc,
			want:   "fc00:1000:9600::ffff:a14:3d8",
		},
		{
			// asnEnc = (71<<20)|(1<<19)|(0x4a) = 0x0478004a ⇒ 04 78 00 4a.
			name:   "hex-range asn v4 host",
			ia:     "71-2:0:4a",
			host:   "1.2.3.4",
			prefix: 0xfc,
			want:   "fc04:7800:4a00::ffff:102:304",
		},
		{
			// v6 host copies its low 8 bytes; local-prefix bytes stay zero.
			name:   "bgp-asn v6 host",
			ia:     "1-150",
			host:   "2001:db8::1",
			prefix: 0xfc,
			want:   "fc00:1000:9600::1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ScitraMap(tt.ia, netip.MustParseAddr(tt.host), tt.prefix)
			if err != nil {
				t.Fatalf("ScitraMap(%q,%q): %v", tt.ia, tt.host, err)
			}
			if got != netip.MustParseAddr(tt.want) {
				t.Fatalf("ScitraMap(%q,%q) = %s, want %s", tt.ia, tt.host, got, tt.want)
			}
		})
	}
}

func TestScitraMapUnmappable(t *testing.T) {
	tests := []struct {
		name string
		ia   string
	}{
		// ASN 0x80000 = 524288: >= 2^19 but below the BGP hex range window.
		{name: "asn out of range", ia: "1-0:8:0"},
		// ISD 4096 = 2^12: too large to encode in 12 bits.
		{name: "isd too large", ia: "4096-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ScitraMap(tt.ia, netip.MustParseAddr("10.0.0.1"), 0xfc); err == nil {
				t.Fatalf("ScitraMap(%q) = nil error, want unmappable error", tt.ia)
			}
		})
	}
}

func TestScitraAvailable(t *testing.T) {
	dir := t.TempDir()

	present := filepath.Join(dir, "present")
	// One realistic fc00::/8 line (dest network, plen 08, then routing fields).
	writeFile(t, present, "fc000000000000000000000000000000 08 00000000000000000000000000000000 00 "+
		"00000000000000000000000000000000 00000064 00000001 00000000 00000001 sci01\n")

	absent := filepath.Join(dir, "absent")
	// Only a default ::/0 and a link-local fe80::/64 route, no fc00.
	writeFile(t, absent, "00000000000000000000000000000000 00 00000000000000000000000000000000 00 "+
		"00000000000000000000000000000000 ffffffff 00000000 00000000 00000000 lo\n"+
		"fe800000000000000000000000000000 40 00000000000000000000000000000000 00 "+
		"00000000000000000000000000000000 00000100 00000001 00000000 00000001 eth0\n")

	orig := procNetIPv6Route
	t.Cleanup(func() { procNetIPv6Route = orig })

	procNetIPv6Route = present
	if !scitraAvailable() {
		t.Fatal("scitraAvailable() = false with fc00::/8 route present")
	}
	procNetIPv6Route = absent
	if scitraAvailable() {
		t.Fatal("scitraAvailable() = true with no fc00::/8 route")
	}
	procNetIPv6Route = filepath.Join(dir, "does-not-exist")
	if scitraAvailable() {
		t.Fatal("scitraAvailable() = true when routing table is unreadable")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
