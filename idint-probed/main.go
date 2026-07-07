// Command idint-probed runs ID-INT probes on demand and serves the results
// over HTTP (GET /api/v1/paths, POST /api/v1/probe) for fabricd.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"net/netip"

	"github.com/netsys-lab/ietf-scion-testbed/idint-probed/prober"
	"github.com/netsys-lab/ietf-scion-testbed/idint-probed/server"
)

func main() {
	var (
		listen     string
		sciondAddr string
	)
	flag.StringVar(&listen, "listen", "", "HTTP listen address (ip:port), required; "+
		"the IP is also the local bind address for probes")
	flag.StringVar(&sciondAddr, "sciond", "", "SCION daemon address (ip:port), required")
	flag.Parse()

	if listen == "" || sciondAddr == "" {
		flag.Usage()
		log.Fatal("-listen and -sciond are required")
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		log.Fatalf("bad -listen %q: %v", listen, err)
	}
	local, err := netip.ParseAddr(host)
	if err != nil {
		log.Fatalf("bad -listen host %q: %v", host, err)
	}

	// On failure exit non-zero; systemd Restart=always retries until sciond
	// is reachable.
	n, err := prober.Connect(context.Background(), sciondAddr, local)
	if err != nil {
		log.Fatalf("connecting to sciond %s: %v", sciondAddr, err)
	}
	log.Printf("idint-probed: local IA %s, probes from %s, sciond %s, listening on %s",
		n.LocalIA, local, sciondAddr, listen)

	log.Fatal(http.ListenAndServe(listen, (&server.Server{Engine: n}).Handler()))
}
