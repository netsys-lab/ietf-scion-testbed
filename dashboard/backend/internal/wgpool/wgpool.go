// Package wgpool manages the pool of pre-provisioned WireGuard client
// configs attendees claim through the join flow. Plan A deploys the pool
// file (/var/lib/fabricd/wg-pool.json) and the WG hub; this package (B3)
// loads, claims from, and persists that pool. For B1 it holds only the
// wire type join handlers pass around and the exhaustion error, so
// internal/api can compile against api.PoolStore before B3 lands.
package wgpool

import "errors"

// Slot is one pre-provisioned WireGuard client identity from the pool file.
type Slot struct {
	N          int    `json:"n"`
	IP         string `json:"ip"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
}

// ErrExhausted is returned by Claim when no unclaimed slot remains.
var ErrExhausted = errors.New("wg conf pool exhausted")
