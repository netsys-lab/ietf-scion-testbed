// Package topowriter maintains the border router's topology.json idint.speed:
// the deploy-time nominal topology with each shaped interface's idint.speed
// overridden from the live tc rate, so the BR's ID-INT utilization meter
// tracks the shaped link capacity. The ietf-126 BR reloads these on SIGHUP
// (commit e356d834b, gated on experimental_idint). Mirrors staticinfo.Writer.
package topowriter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sync"

	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/shape"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/staticinfo"
)

type Writer struct {
	BasePath string
	OutPath  string
	Unit     string                  // systemd unit to SIGHUP; empty = skip
	Signal   func(unit string) error // nil = staticinfo.DefaultSignal

	mu                sync.Mutex
	metaFail, hupFail bool
}

// Write rebuilds OutPath from BasePath and the live per-ifid tc state, then
// signals the BR. Only border_routers.*.interfaces.<ifid>.idint.speed of a
// shaped interface (RateMbit set) changes; every other field passes through.
func (w *Writer) Write(live map[string]shape.Params) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	doc, err := w.merged(live)
	if err != nil {
		w.metaFail = true
		w.hupFail = true // no signal attempted: don't report a stale reload_ok
		return err
	}
	// Byte-identical to what's on disk: skip the write and the BR signal so a
	// crash-looping linkd that reconverges on the same state doesn't SIGHUP
	// the router every restart, and a no-op OnChange is free.
	if existing, err := os.ReadFile(w.OutPath); err == nil &&
		bytes.Equal(bytes.TrimSuffix(existing, []byte("\n")), doc) {
		w.metaFail = false
		w.hupFail = false
		return nil
	}
	if err := staticinfo.WriteAtomic(w.OutPath, doc); err != nil {
		w.metaFail = true
		w.hupFail = true
		return err
	}
	w.metaFail = false

	if w.Unit == "" {
		w.hupFail = false
		return nil
	}
	sig := w.Signal
	if sig == nil {
		sig = staticinfo.DefaultSignal
	}
	if err := sig(w.Unit); err != nil {
		w.hupFail = true
		return err
	}
	w.hupFail = false
	return nil
}

// Status reports the outcome of the last Write.
func (w *Writer) Status() (metadataOK, reloadOK bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.metaFail, !w.hupFail
}

func (w *Writer) merged(live map[string]shape.Params) ([]byte, error) {
	raw, err := os.ReadFile(w.BasePath)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", w.BasePath, err)
	}
	brs, _ := doc["border_routers"].(map[string]any)
	for _, brAny := range brs {
		br, _ := brAny.(map[string]any)
		if br == nil {
			continue
		}
		ifaces, _ := br["interfaces"].(map[string]any)
		for ifid, ifAny := range ifaces {
			p, ok := live[ifid]
			if !ok || p.RateMbit == nil || *p.RateMbit <= 0 {
				continue // keep the base's nominal idint.speed
			}
			iface, _ := ifAny.(map[string]any)
			if iface == nil {
				continue
			}
			idint, _ := iface["idint"].(map[string]any)
			if idint == nil {
				idint = map[string]any{}
				iface["idint"] = idint
			}
			idint["speed"] = int64(math.Round(*p.RateMbit * 1e6)) // Mbit/s -> bits/s
		}
	}
	return json.MarshalIndent(doc, "", " ")
}
