// Command scion-linkd shapes SCION inter-AS links via netem,
// controlled over a REST API on the management network.
package main

import (
	"flag"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/api"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/baseline"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/bgpstatus"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/config"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/shape"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/staticinfo"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/topo"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/topowriter"
)

// reloadCoalesceWindow bounds how often a burst of dashboard edits can
// SIGHUP the control service: at most one immediate reload plus one
// trailing reload per window, instead of one per click.
const reloadCoalesceWindow = time.Second

func main() {
	cfgPath := flag.String("config", "/etc/scion-linkd/config.toml", "config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ifs, err := topo.Load(cfg.TopologyGlob)
	if err != nil {
		log.Fatalf("topology: %v", err)
	}
	var managed []api.ManagedIface
	for _, i := range ifs {
		dev, err := shape.DevByAddr(i.LocalIP)
		if err != nil {
			log.Printf("skip if %s (%s): %v", i.IfID, i.LocalIP, err)
			continue
		}
		managed = append(managed, api.ManagedIface{Interface: i, Dev: dev})
		log.Printf("managing if %s -> %s (%s, %s)", i.IfID, dev, i.Neighbor, i.LinkTo)
	}
	if len(managed) == 0 {
		log.Fatal("no shapeable interfaces found")
	}

	devByIfid := make(map[string]string, len(managed))
	for _, m := range managed {
		devByIfid[m.IfID] = m.Dev
	}
	bgpCol := bgpstatus.New(devByIfid)

	// --- beacon metadata sync (all optional: missing files degrade gracefully) ---
	var (
		blm        map[string]shape.Params
		writer     *staticinfo.Writer
		topoWriter *topowriter.Writer
	)
	if p, err := baseline.ResolveOne(cfg.BaselineProfile); err != nil {
		log.Printf("baseline profile disabled: %v", err)
	} else if blm, err = baseline.Load(p); err != nil {
		// Bad profile must not crash-loop the daemon: shaping is the primary
		// duty. Run without baseline (DELETE falls back to clear).
		log.Printf("baseline profile disabled: %s: %v", p, err)
		blm = nil
	}
	if bp, err := baseline.ResolveOne(cfg.StaticinfoBase); err != nil {
		log.Printf("static info sync disabled: %v", err)
	} else {
		out := cfg.StaticinfoOut
		if out == "" {
			out = strings.TrimSuffix(bp, ".base.json") + ".json"
		}
		writer = &staticinfo.Writer{BasePath: bp, OutPath: out, Unit: cfg.CSReloadUnit}
	}
	if tp, err := baseline.ResolveOne(cfg.TopologyBase); err != nil {
		log.Printf("topology speed sync disabled: %v", err)
	} else {
		out := cfg.TopologyOut
		if out == "" {
			out = strings.TrimSuffix(tp, ".base.json") + ".json"
		}
		topoWriter = &topowriter.Writer{BasePath: tp, OutPath: out, Unit: cfg.BRReloadUnit}
	}

	shaper := shape.NewNetlinkShaper()

	// Preshape: story baseline on every interface that has no qdisc yet.
	// Idempotent across restarts — live shaping is never clobbered.
	for _, m := range managed {
		bp, ok := blm[m.IfID]
		if !ok {
			continue
		}
		if cur, err := shaper.Get(m.Dev); err != nil || !cur.Empty() {
			continue
		}
		if err := shaper.Apply(m.Dev, bp); err != nil {
			log.Printf("preshape %s (%s): %v", m.IfID, m.Dev, err)
		} else {
			log.Printf("preshaped %s (%s) to story baseline", m.IfID, m.Dev)
		}
	}

	regen := func() {}
	var status func() (bool, bool)
	if writer != nil || topoWriter != nil {
		regen = func() {
			live := map[string]shape.Params{}
			for _, m := range managed {
				if p, err := shaper.Get(m.Dev); err == nil {
					live[m.IfID] = p
				}
			}
			if writer != nil {
				if err := writer.Write(live); err != nil {
					log.Printf("static info: %v", err)
				}
			}
			if topoWriter != nil {
				if err := topoWriter.Write(live); err != nil {
					log.Printf("topology speed: %v", err)
				}
			}
		}
		status = func() (bool, bool) {
			m, r := true, true
			if writer != nil {
				wm, wr := writer.Status()
				m, r = m && wm, r && wr
			}
			if topoWriter != nil {
				tm, tr := topoWriter.Status()
				m, r = m && tm, r && tr
			}
			return m, r
		}
		regen() // converge on startup
	}

	// regen already ran once directly above to converge on startup; OnChange
	// gets a coalesced wrapper so rapid API edits collapse into at most one
	// immediate + one trailing regen (and CS signal) per window.
	onChange := staticinfo.Coalesce(reloadCoalesceWindow, regen)

	log.Printf("scion-linkd listening on %s, %d interfaces", cfg.Listen, len(managed))
	log.Fatal(http.ListenAndServe(cfg.Listen, api.New(managed, shaper,
		api.Options{Baseline: blm, OnChange: onChange, Status: status, BGPSessions: bgpCol.Sessions})))
}
