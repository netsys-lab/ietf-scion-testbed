// Package bgpstatus reads BIRD's BGP + BFD session state via birdc for
// linkd's GET /api/v1/bgp. Protocol names are gen_bird.py's bgp_if<ifid>;
// timestamps are pinned by `timeformat protocol iso long;` in bird.conf.
package bgpstatus

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Session struct {
	IfID  string `json:"ifid"`
	State string `json:"state"` // BIRD Info column; "Established" when up
	BFD   string `json:"bfd"`   // "Up"/"Down"; "" if no BFD row matched
	Since int64  `json:"since_unix"`
}

// Route is one fabric-prefix best route: the local BGP session (ifid) that
// currently carries traffic toward prefix_as's 10.<n>.0.0/16.
type Route struct {
	PrefixAS int    `json:"prefix_as"`
	IfID     string `json:"ifid"`
}

// Snapshot is the full /api/v1/bgp payload: session states plus per-prefix
// best-route egress. Both parsed from birdc in one cached refresh.
type Snapshot struct {
	Sessions []Session `json:"sessions"`
	Routes   []Route   `json:"routes"`
}

type Collector struct {
	run       func(cmd string, args ...string) ([]byte, error)
	ifidByDev map[string]string // "sci1" -> "65377"
	ttl       time.Duration

	mu        sync.Mutex
	cached    Snapshot
	fetchedAt time.Time
}

func New(devByIfid map[string]string) *Collector {
	inv := make(map[string]string, len(devByIfid))
	for ifid, dev := range devByIfid {
		inv[dev] = ifid
	}
	return &Collector{run: runBirdc, ifidByDev: inv, ttl: 2 * time.Second}
}

func runBirdc(cmd string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, cmd, args...).Output()
}

// Snapshot returns the current BGP sessions and fabric-prefix best routes,
// cached for ttl so dashboard polling cannot stampede BIRD.
func (c *Collector) Snapshot() (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.fetchedAt.IsZero() && time.Since(c.fetchedAt) < c.ttl {
		return c.cached, nil
	}
	proto, err := c.run("birdc", "-r", "show", "protocols")
	if err != nil {
		return Snapshot{}, fmt.Errorf("birdc show protocols: %w", err)
	}
	bfd, err := c.run("birdc", "-r", "show", "bfd", "sessions")
	if err != nil {
		return Snapshot{}, fmt.Errorf("birdc show bfd sessions: %w", err)
	}
	routes, err := c.run("birdc", "-r", "show", "route", "primary")
	if err != nil {
		return Snapshot{}, fmt.Errorf("birdc show route primary: %w", err)
	}
	ss := parseProtocols(proto)
	bfdStates := parseBFD(bfd, c.ifidByDev)
	for i := range ss {
		ss[i].BFD = bfdStates[ss[i].IfID]
	}
	c.cached = Snapshot{Sessions: ss, Routes: parseRoutes(routes)}
	c.fetchedAt = time.Now()
	return c.cached, nil
}

func parseProtocols(out []byte) []Session {
	var ss []Session
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[1] != "BGP" || !strings.HasPrefix(f[0], "bgp_if") {
			continue
		}
		s := Session{IfID: strings.TrimPrefix(f[0], "bgp_if"), State: f[3]}
		if len(f) >= 6 {
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", f[4]+" "+f[5], time.Local); err == nil {
				s.Since = t.Unix()
			}
		}
		if len(f) >= 7 {
			s.State = f[6] // Info column, e.g. "Established", "Active"
		}
		ss = append(ss, s)
	}
	return ss
}

// parseRoutes extracts each fabric v4 prefix's best-route egress session from
// `birdc -r show route primary`. Route lines carry the learning protocol's
// name (gen_bird.py's bgp_if<ifid>) inline — e.g.
// `10.156.0.0/16  unicast [bgp_if48610 14:40:28.269] * (100) [AS156i]` — so
// the ifid comes straight off the route line; no next-hop/device parsing.
// Non-BGP bests (the local AS's own blackhole/originate, the WG anycast /24)
// don't match and are simply absent: absence means "no BGP best route", and
// fabricd's walker truncates there.
func parseRoutes(out []byte) []Route {
	var rs []Route
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != "unicast" || !strings.HasPrefix(f[2], "[bgp_if") {
			continue
		}
		rest, ok := strings.CutPrefix(f[0], "10.")
		if !ok {
			continue
		}
		asStr, ok := strings.CutSuffix(rest, ".0.0/16")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(asStr)
		if err != nil {
			continue
		}
		rs = append(rs, Route{PrefixAS: n, IfID: strings.TrimPrefix(f[2], "[bgp_if")})
	}
	return rs
}

func parseBFD(out []byte, ifidByDev map[string]string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		if ifid, ok := ifidByDev[f[1]]; ok {
			m[ifid] = f[2]
		}
	}
	return m
}
