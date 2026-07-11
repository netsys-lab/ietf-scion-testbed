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

func fakeRun(proto, bfd []byte, err error) func(string, ...string) ([]byte, error) {
	return func(cmd string, args ...string) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		for _, a := range args {
			if a == "bfd" {
				return bfd, nil
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
	c := newTest(fakeRun([]byte(protoOut), []byte(bfdOut), nil))
	ss, err := c.Sessions()
	if err != nil {
		t.Fatal(err)
	}
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
}

func TestSessionsBirdcError(t *testing.T) {
	c := newTest(fakeRun(nil, nil, errors.New("exec: birdc: not found")))
	if _, err := c.Sessions(); err == nil {
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
		}
		return []byte(protoOut), nil
	})
	if _, err := c.Sessions(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Sessions(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 { // one protocols + one bfd; second Sessions() hits the cache
		t.Fatalf("want 2 birdc calls, got %d", calls)
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
