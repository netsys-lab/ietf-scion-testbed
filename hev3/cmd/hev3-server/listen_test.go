package main

import (
	"errors"
	"net"
	"net/netip"
	"reflect"
	"testing"
)

// synthAddrs returns an address enumerator (matching net.InterfaceAddrs'
// signature) that serves a fixed synthetic list, for tests that must not
// depend on the machine's real interfaces.
func synthAddrs(cidrs ...string) func() ([]net.Addr, error) {
	return func() ([]net.Addr, error) {
		out := make([]net.Addr, 0, len(cidrs))
		for _, c := range cidrs {
			ip, ipNet, err := net.ParseCIDR(c)
			if err != nil {
				panic(err)
			}
			ipNet.IP = ip
			out = append(out, ipNet)
		}
		return out, nil
	}
}

// erroringAddrs fails the test if it is ever invoked: used to prove the
// explicit-host and explicit-equals-scion paths short-circuit before
// enumerating interfaces.
func erroringAddrs(t *testing.T) func() ([]net.Addr, error) {
	t.Helper()
	return func() ([]net.Addr, error) {
		t.Fatal("ipH3Addrs: address enumerator must not be called")
		return nil, nil
	}
}

func TestIPH3Addrs_WildcardExcludesSCIONIncludesLoopback(t *testing.T) {
	scionIP := netip.MustParseAddr("10.20.3.150")
	lister := synthAddrs(
		"10.20.3.150/24", // SCION underlay IP: must be excluded
		"10.20.3.151/24", // another global unicast: must be included
		"127.0.0.1/8",    // loopback: must be included
		"::1/128",        // loopback: must be included
	)

	got, err := ipH3Addrs("", "443", scionIP, lister)
	if err != nil {
		t.Fatalf("ipH3Addrs: unexpected error: %v", err)
	}
	want := []string{
		"10.20.3.151:443",
		"127.0.0.1:443",
		"[::1]:443",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ipH3Addrs = %v, want %v", got, want)
	}
}

func TestIPH3Addrs_LinkLocalExcluded(t *testing.T) {
	scionIP := netip.MustParseAddr("10.20.3.150")
	lister := synthAddrs(
		"10.20.3.151/24", // global unicast: included
		"169.254.1.5/16", // IPv4 link-local: excluded
		"fe80::1/64",     // IPv6 link-local: excluded
	)

	got, err := ipH3Addrs("", "8443", scionIP, lister)
	if err != nil {
		t.Fatalf("ipH3Addrs: unexpected error: %v", err)
	}
	want := []string{"10.20.3.151:8443"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ipH3Addrs = %v, want %v (link-local entries must be excluded)", got, want)
	}
}

func TestIPH3Addrs_ExplicitHostHonored(t *testing.T) {
	scionIP := netip.MustParseAddr("10.20.3.150")

	got, err := ipH3Addrs("10.20.3.151", "443", scionIP, erroringAddrs(t))
	if err != nil {
		t.Fatalf("ipH3Addrs: unexpected error: %v", err)
	}
	want := []string{"10.20.3.151:443"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ipH3Addrs = %v, want %v", got, want)
	}
}

func TestIPH3Addrs_ExplicitEqualsSCIONErrors(t *testing.T) {
	scionIP := netip.MustParseAddr("10.20.3.150")

	_, err := ipH3Addrs("10.20.3.150", "443", scionIP, erroringAddrs(t))
	if err == nil {
		t.Fatal("ipH3Addrs: want error when -listen-ip host equals the SCION underlay address, got nil")
	}
}

// TestIPH3Addrs_ExplicitEqualsSCION_IPv4MappedForm exercises the same
// collision via an IPv4-in-IPv6 spelling, to pin that comparison is done on
// the unmapped address rather than the raw string/netip.Addr form.
func TestIPH3Addrs_ExplicitEqualsSCION_IPv4MappedForm(t *testing.T) {
	scionIP := netip.MustParseAddr("::ffff:10.20.3.150")

	_, err := ipH3Addrs("10.20.3.150", "443", scionIP, erroringAddrs(t))
	if err == nil {
		t.Fatal("ipH3Addrs: want error for IPv4-mapped SCION IP equal to explicit host, got nil")
	}
}

func TestIPH3Addrs_NoSCIONIsWildcardUnchanged(t *testing.T) {
	got, err := ipH3Addrs("", "443", netip.Addr{}, erroringAddrs(t))
	if err != nil {
		t.Fatalf("ipH3Addrs: unexpected error: %v", err)
	}
	want := []string{":443"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ipH3Addrs = %v, want %v", got, want)
	}
}

func TestIPH3Addrs_ListerErrorPropagates(t *testing.T) {
	scionIP := netip.MustParseAddr("10.20.3.150")
	wantErr := errors.New("boom")
	lister := func() ([]net.Addr, error) { return nil, wantErr }

	_, err := ipH3Addrs("", "443", scionIP, lister)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ipH3Addrs error = %v, want wrapping %v", err, wantErr)
	}
}

func TestIPH3Addrs_EmptyAfterExclusionErrors(t *testing.T) {
	scionIP := netip.MustParseAddr("10.20.3.150")
	lister := synthAddrs("10.20.3.150/24", "fe80::1/64")

	_, err := ipH3Addrs("", "443", scionIP, lister)
	if err == nil {
		t.Fatal("ipH3Addrs: want error when nothing is left to bind, got nil")
	}
}

func TestIsExplicitHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"", false},
		{"0.0.0.0", false},
		{"::", false},
		{"127.0.0.1", true},
		{"10.20.3.150", true},
		{"::1", true},
	}
	for _, tc := range cases {
		if got := isExplicitHost(tc.host); got != tc.want {
			t.Errorf("isExplicitHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
