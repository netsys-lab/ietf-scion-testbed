package main

import (
	"errors"
	"net"
	"testing"
)

func TestScionLocalIPFlagWins(t *testing.T) {
	auto := func() (net.IP, error) { t.Fatal("auto must not be called"); return nil, nil }
	ip, err := scionLocalIP("10.150.0.81", auto)
	if err != nil || !ip.Equal(net.ParseIP("10.150.0.81")) {
		t.Fatalf("got %v, %v", ip, err)
	}
}

func TestScionLocalIPInvalidFlag(t *testing.T) {
	if _, err := scionLocalIP("not-an-ip", nil); err == nil {
		t.Fatal("want error for invalid -scion-ip")
	}
}

func TestScionLocalIPEmptyFallsBack(t *testing.T) {
	want := net.ParseIP("10.20.3.215")
	ip, err := scionLocalIP("", func() (net.IP, error) { return want, nil })
	if err != nil || !ip.Equal(want) {
		t.Fatalf("got %v, %v", ip, err)
	}
	if _, err := scionLocalIP("", func() (net.IP, error) { return nil, errors.New("boom") }); err == nil {
		t.Fatal("want auto error propagated")
	}
}
