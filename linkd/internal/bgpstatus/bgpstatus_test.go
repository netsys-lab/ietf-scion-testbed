package bgpstatus

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

const protoOut = `BIRD 2.15 ready.
Name       Proto      Table      State  Since         Info
device1    Device     ---        up     2026-07-11 10:00:00
kernel4    Kernel     master4    up     2026-07-11 10:00:00
bgp_if65377 BGP        ---        up     2026-07-11 14:22:33  Established
bgp_if18982 BGP        ---        start  2026-07-11 14:25:01  Active        Socket: Connection refused
`

const bfdOut = `BIRD 2.15 ready.
bfd1:
 IP address                Interface  State      Since         Interval  Timeout
 fd00:fade:1::151          sci1       Up         2026-07-11 14:22:35    0.500    2.000
 fd00:fade:2::152          sci2       Down       2026-07-11 14:25:00    0.500    2.000
`

const routeOut = `BIRD 2.14 ready.
Access restricted
Table master4:
10.156.0.0/16        unicast [bgp_if48610 14:40:28.269] * (100) [AS156i]
	via fd00:fade:13::159 on sci13
10.153.0.0/16        unicast [bgp_if59691 15:24:57.010] * (100) [AS153i]
	via fd00:fade:f::154 on sciF
10.20.5.0/24         unicast [originate4 14:40:24.050] * (200)
	via 10.20.3.201 on eth0
10.158.0.0/16        blackhole [originate4 14:40:24.050] * (200)
10.155.0.0/16        unicast [bgp_if48164 14:40:25.460] * (100) [AS155i]
	via fd00:fade:11::155 on sci11

Table master6:
fd00:beef:153::/48   unicast [bgp_if59691 15:24:57.036] * (100) [AS153i]
	via fd00:fade:f::154 on sciF
fd00:beef:158::/48   blackhole [originate6 14:40:24.050] * (200)
`

func TestParseRoutes(t *testing.T) {
	rs := parseRoutes([]byte(routeOut))
	want := []Route{{156, "48610"}, {153, "59691"}, {155, "48164"}}
	if len(rs) != len(want) {
		t.Fatalf("want %d routes, got %d: %+v", len(want), len(rs), rs)
	}
	for i, r := range rs {
		if r != want[i] {
			t.Fatalf("route %d: got %+v want %+v", i, r, want[i])
		}
	}
}

func TestRouteJSONKeys(t *testing.T) {
	b, err := json.Marshal(Route{PrefixAS: 150, IfID: "28417"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"prefix_as":150,"ifid":"28417"}`
	if string(b) != want {
		t.Fatalf("wire format drifted:\n got %s\nwant %s", b, want)
	}
}

func fakeRun(proto, bfd, routes []byte, err error) func(string, ...string) ([]byte, error) {
	return func(cmd string, args ...string) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		for _, a := range args {
			if a == "bfd" {
				return bfd, nil
			}
			if a == "route" {
				return routes, nil
			}
		}
		return proto, nil
	}
}

func newTest(run func(string, ...string) ([]byte, error)) *Collector {
	c := New(map[string]string{"65377": "sci1", "18982": "sci2"})
	c.run = run
	return c
}

func TestSessionsParse(t *testing.T) {
	c := newTest(fakeRun([]byte(protoOut), []byte(bfdOut), []byte(routeOut), nil))
	snap, err := c.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	ss := snap.Sessions
	if len(ss) != 2 {
		t.Fatalf("want 2 sessions, got %d: %+v", len(ss), ss)
	}
	up := ss[0]
	if up.IfID != "65377" || up.State != "Established" || up.BFD != "Up" {
		t.Fatalf("session 0: %+v", up)
	}
	want, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-07-11 14:22:33", time.Local)
	if up.Since != want.Unix() {
		t.Fatalf("since: got %d want %d", up.Since, want.Unix())
	}
	down := ss[1]
	if down.IfID != "18982" || down.State != "Active" || down.BFD != "Down" {
		t.Fatalf("session 1: %+v", down)
	}
	if len(snap.Routes) != 3 {
		t.Fatalf("want 3 routes, got %d: %+v", len(snap.Routes), snap.Routes)
	}
}

func TestSessionsBirdcError(t *testing.T) {
	c := newTest(fakeRun(nil, nil, nil, errors.New("exec: birdc: not found")))
	if _, err := c.Snapshot(); err == nil {
		t.Fatal("want error when birdc fails")
	}
}

func TestSessionsCached(t *testing.T) {
	calls := 0
	c := newTest(func(cmd string, args ...string) ([]byte, error) {
		calls++
		for _, a := range args {
			if a == "bfd" {
				return []byte(bfdOut), nil
			}
			if a == "route" {
				return []byte(routeOut), nil
			}
		}
		return []byte(protoOut), nil
	})
	if _, err := c.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Snapshot(); err != nil {
		t.Fatal(err)
	}
	if calls != 3 { // protocols + bfd + route; second Snapshot() hits the cache
		t.Fatalf("want 3 birdc calls, got %d", calls)
	}
}

func TestSessionJSONKeys(t *testing.T) {
	b, err := json.Marshal(Session{IfID: "65377", State: "Established", BFD: "Up", Since: 1770000000})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"ifid":"65377","state":"Established","bfd":"Up","since_unix":1770000000}`
	if string(b) != want {
		t.Fatalf("wire format drifted:\n got %s\nwant %s", b, want)
	}
}
