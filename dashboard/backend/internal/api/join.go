package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/ratelimit"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/scitramap"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/wgpool"
)

// renderConf renders a client .conf for slot with the given endpoint host
// ("[v6]" bracketing is the caller's job via endpointStr).
func renderConf(sl wgpool.Slot, serverPub, endpoint string) string {
	return fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/32
DNS = 10.20.3.216
MTU = 1380

[Peer]
PublicKey = %s
AllowedIPs = 10.20.3.0/24, 10.20.5.0/24
Endpoint = %s
PersistentKeepalive = 25
`, sl.PrivateKey, sl.IP, serverPub, endpoint)
}

func (jc JoinConfig) endpointV6Str() string {
	if jc.EndpointV6 == "" {
		return ""
	}
	return fmt.Sprintf("[%s]:%d", jc.EndpointV6, jc.ListenPort)
}

func (jc JoinConfig) endpointV4Str() string {
	if jc.EndpointV4 == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", jc.EndpointV4, jc.ListenPort)
}

func (s *server) handleJoinClaim(w http.ResponseWriter, r *http.Request) {
	if !s.join.Enabled {
		http.NotFound(w, r)
		return
	}
	if s.pool == nil {
		http.Error(w, "conf pool unavailable", http.StatusServiceUnavailable)
		return
	}
	if !s.limiter.Allow(ratelimit.ClientKey(r.RemoteAddr)) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	var req struct {
		AS   int    `json:"as"`
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(req.Code), []byte(s.join.BoothCode)) != 1 {
		http.Error(w, "bad code", http.StatusForbidden)
		return
	}
	// AS is optional now: one conf tunnels the whole mgmt net, so the slot is
	// not bound to a single AS. Default to the first joinable AS; if a
	// specific AS is given it must still be joinable.
	if req.AS == 0 && len(s.join.JoinableASes) > 0 {
		req.AS = s.join.JoinableASes[0]
	}
	if !s.join.asAllowed(req.AS) {
		http.NotFound(w, r)
		return
	}
	sl, err := s.pool.Claim(req.AS)
	if err == wgpool.ErrExhausted {
		http.Error(w, "no confs left — ask at the booth", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "pool error", http.StatusInternalServerError)
		return
	}
	ip, err := netip.ParseAddr(sl.IP)
	if err != nil {
		http.Error(w, "pool error", http.StatusInternalServerError)
		return
	}
	fc, err := scitramap.MappedIPv6(s.join.ISD, req.AS, ip)
	if err != nil {
		http.Error(w, "identity derivation failed", http.StatusInternalServerError)
		return
	}
	// The claimed slot's single WireGuard IP maps to a distinct fc00
	// identity per joinable AS (scitra keys identity off ISD-AS + host, not
	// the WG address alone), so surface all of them: the attendee can act as
	// an endhost in any joinable AS over the one conf.
	identities := make(map[string]string, len(s.join.JoinableASes))
	for _, as := range s.join.JoinableASes {
		id, err := scitramap.MappedIPv6(s.join.ISD, as, ip)
		if err != nil {
			http.Error(w, "identity derivation failed", http.StatusInternalServerError)
			return
		}
		identities[strconv.Itoa(as)] = id.String()
	}
	resp := map[string]any{
		"slot": sl.N, "ip": sl.IP, "as": req.AS,
		"isd_as":          fmt.Sprintf("%d-%d", s.join.ISD, req.AS),
		"fc00_identity":   fc.String(),
		"fc00_identities": identities,
		"conf":            renderConf(sl, s.pool.ServerPublicKey(), s.join.endpointV6Str()),
		"endpoint_v6":     s.join.endpointV6Str(),
	}
	if v4 := s.join.endpointV4Str(); v4 != "" {
		resp["conf_v4"] = renderConf(sl, s.pool.ServerPublicKey(), v4)
		resp["endpoint_v4"] = v4
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleJoinMeta(w http.ResponseWriter, r *http.Request) {
	if !s.join.Enabled {
		http.NotFound(w, r)
		return
	}
	total, claimed, burned := 0, 0, 0
	if s.pool != nil {
		total, claimed, burned = s.pool.Stats()
	}
	hubOK := false
	if s.join.HubProbeAddr != "" {
		if c, err := net.DialTimeout("tcp", s.join.HubProbeAddr, time.Second); err == nil {
			c.Close()
			hubOK = true
		}
	}
	// Playground (browser-shell) ASes are the containers behind the /play
	// proxy — fixed at 158-161, independent of the WireGuard joinable set.
	playAses := make([]int, 0, len(s.join.PlayTargets))
	for as := range s.join.PlayTargets {
		playAses = append(playAses, as)
	}
	sort.Ints(playAses)

	// joinable carries per-AS join info (bundle + bootstrap URLs) alongside
	// the plain joinable_ases list kept for backward compatibility.
	joinable := make([]map[string]any, 0, len(s.join.JoinableASes))
	for _, as := range s.join.JoinableASes {
		joinable = append(joinable, map[string]any{
			"as":            as,
			"isd_as":        fmt.Sprintf("%d-%d", s.join.ISD, as),
			"bundle_url":    fmt.Sprintf("/api/join/bundle/%d", as),
			"bootstrap_url": s.join.bootstrapURL(as),
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":         true,
		"joinable_ases":   s.join.JoinableASes,
		"joinable":        joinable,
		"playground_ases": playAses,
		"slots_total":     total, "slots_claimed": claimed, "slots_burned": burned,
		"hub_ok":      hubOK,
		"endpoint_v6": s.join.endpointV6Str(),
		"endpoint_v4": s.join.endpointV4Str(),
	})
}
