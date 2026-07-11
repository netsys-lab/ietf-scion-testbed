// Package bgpstatus reads BIRD's BGP + BFD session state via birdc for
// linkd's GET /api/v1/bgp. Protocol names are gen_bird.py's bgp_if<ifid>;
// timestamps are pinned by `timeformat protocol iso long;` in bird.conf.
package bgpstatus

import (
	"context"
	"fmt"
	"os/exec"
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

type Collector struct {
	run       func(cmd string, args ...string) ([]byte, error)
	ifidByDev map[string]string // "sci1" -> "65377"
	ttl       time.Duration

	mu        sync.Mutex
	cached    []Session
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

// Sessions returns the current BGP sessions, cached for ttl so dashboard
// polling cannot stampede BIRD.
func (c *Collector) Sessions() ([]Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached != nil && time.Since(c.fetchedAt) < c.ttl {
		return c.cached, nil
	}
	proto, err := c.run("birdc", "-r", "show", "protocols")
	if err != nil {
		return nil, fmt.Errorf("birdc show protocols: %w", err)
	}
	bfd, err := c.run("birdc", "-r", "show", "bfd", "sessions")
	if err != nil {
		return nil, fmt.Errorf("birdc show bfd sessions: %w", err)
	}
	ss := parseProtocols(proto)
	bfdStates := parseBFD(bfd, c.ifidByDev)
	for i := range ss {
		ss[i].BFD = bfdStates[ss[i].IfID]
	}
	c.cached, c.fetchedAt = ss, time.Now()
	return ss, nil
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
