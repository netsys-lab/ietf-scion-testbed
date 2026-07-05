// Command scion-linkd shapes SCION inter-AS links via netem,
// controlled over a REST API on the management network.
package main

import (
	"flag"
	"log"
	"net/http"
	"strings"

	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/api"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/baseline"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/config"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/shape"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/staticinfo"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/topo"
)

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

	// --- beacon metadata sync (all optional: missing files degrade gracefully) ---
	var (
		blm    map[string]shape.Params
		writer *staticinfo.Writer
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
	if writer != nil {
		regen = func() {
			live := map[string]shape.Params{}
			for _, m := range managed {
				if p, err := shaper.Get(m.Dev); err == nil {
					live[m.IfID] = p
				}
			}
			if err := writer.Write(live); err != nil {
				log.Printf("static info: %v", err)
			}
		}
		status = writer.Status
		regen() // converge on startup
	}

	log.Printf("scion-linkd listening on %s, %d interfaces", cfg.Listen, len(managed))
	log.Fatal(http.ListenAndServe(cfg.Listen, api.New(managed, shaper,
		api.Options{Baseline: blm, OnChange: regen, Status: status})))
}
