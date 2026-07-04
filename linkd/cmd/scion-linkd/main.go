// Command scion-linkd shapes SCION inter-AS links via netem,
// controlled over a REST API on the management network.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/api"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/config"
	"github.com/netsys-lab/ietf-scion-testbed/linkd/internal/shape"
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
	log.Printf("scion-linkd listening on %s, %d interfaces", cfg.Listen, len(managed))
	log.Fatal(http.ListenAndServe(cfg.Listen, api.New(managed, shape.NewNetlinkShaper())))
}
