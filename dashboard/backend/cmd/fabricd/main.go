// Command fabricd is the SCION Fabrik dashboard backend. It builds the
// testbed's topology graph, feeds a ring-buffer Store from either the real
// Prometheus scrapers or (in mock mode) a synthetic generator, derives
// link/AS view models, and serves the dashboard's REST + WebSocket API and
// static frontend.
package main

import (
	"context"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/api"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/derive"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/linkdclient"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/mock"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/scrape"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// storeCapacity is the number of samples retained per store key. At the
// (default) 1s sample cadence, 3600 covers a 1-hour rolling window; the
// API's /api/history endpoint caps requests at 60 minutes.
const storeCapacity = 3600

// scrapeClientTimeout/linkdClientTimeout bound the per-request HTTP calls
// made to the testbed's Prometheus endpoints and to each AS's linkd,
// respectively; both are management-network calls expected to be fast.
const (
	scrapeClientTimeout = 800 * time.Millisecond
	linkdClientTimeout  = 2 * time.Second

	frameInterval = 1 * time.Second
	pollInterval  = 5 * time.Second
)

// config is fabricd's on-disk configuration.
type config struct {
	Listen           string `toml:"listen"`
	ConfigDir        string `toml:"config_dir"`
	StaticDir        string `toml:"static_dir"`
	ScrapeIntervalMs int    `toml:"scrape_interval_ms"`
	Mock             bool   `toml:"mock"`
}

// loadConfig decodes path over these defaults: an empty file is fine
// (defaults apply); a missing file is an error -- the deb always ships
// /etc/fabricd/config.toml.
func loadConfig(path string) (config, error) {
	c := config{
		Listen:           ":8080",
		ConfigDir:        "/etc/fabric/config",
		StaticDir:        "/usr/share/fabricd/web",
		ScrapeIntervalMs: 1000,
		Mock:             false,
	}
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return config{}, err
	}
	return c, nil
}

func main() {
	cfgPath := flag.String("config", "/etc/fabricd/config.toml", "config file")
	flag.Parse()

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	g, err := topo.Load(cfg.ConfigDir)
	if err != nil {
		log.Fatalf("topology: %v", err)
	}
	log.Printf("loaded topology: %d ASes, %d links", len(g.ASes), len(g.Links))

	st := store.New(storeCapacity)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cfg.Mock {
		log.Printf("mock mode: synthesizing telemetry instead of scraping")
		go mock.Run(ctx, g, st)
	} else {
		targets := scrape.Targets(g)
		interval := time.Duration(cfg.ScrapeIntervalMs) * time.Millisecond
		sc := scrape.New(st, targets, interval, &http.Client{Timeout: scrapeClientTimeout})
		go sc.Run(ctx)
	}

	lc := linkdclient.New(g, &http.Client{Timeout: linkdClientTimeout})
	d := derive.New(g, st)

	var static fs.FS
	if info, err := os.Stat(cfg.StaticDir); err == nil && info.IsDir() {
		static = os.DirFS(cfg.StaticDir)
	} else {
		log.Printf("warning: static dir %q not found; static file serving disabled", cfg.StaticDir)
	}

	h := api.New(g, st, d, lc, static)
	go api.RunBroadcast(ctx, h, frameInterval, pollInterval)

	log.Printf("fabricd listening on %s", cfg.Listen)
	log.Fatal(http.ListenAndServe(cfg.Listen, h))
}
