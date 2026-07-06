package scitramap

import (
	"net/netip"
	"testing"
)

func TestMappedIPv6Golden(t *testing.T) {
	got, err := MappedIPv6(1, 158, netip.MustParseAddr("10.20.5.7"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "fc00:1000:9e00::ffff:a14:507"; got.String() != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestMappedIPv6PlaygroundGolden(t *testing.T) {
	got, err := MappedIPv6(1, 161, netip.MustParseAddr("10.20.3.213"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "fc00:1000:a100::ffff:a14:3d5"; got.String() != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestMappedIPv6Limits(t *testing.T) {
	v4 := netip.MustParseAddr("10.0.0.1")
	if _, err := MappedIPv6(1<<12, 1, v4); err == nil {
		t.Fatal("ISD overflow must error")
	}
	if _, err := MappedIPv6(1, 1<<19, v4); err == nil {
		t.Fatal("ASN >= 2^19 must error (BGP-flag bit reserved)")
	}
	if _, err := MappedIPv6(1, 1, netip.MustParseAddr("::1")); err == nil {
		t.Fatal("non-v4 host must error")
	}
}
