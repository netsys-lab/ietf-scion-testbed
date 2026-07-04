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

// demoShapedLink is preshaped (after a warmup delay -- see the mock branch
// below) in mock mode so the dashboard shows a shaped link (elevated band,
// "+12MS" chip) without anyone needing to drive the shaping UI first.
const demoShapedLink = "155-160"

// demoShapedLinkWarmup is how long demoShapedLink runs unshaped before
// SetShaping is applied. derive.Deriver's per-side RTT baseline is the
// minimum ever observed for that key (see internal/derive.(*Deriver).baseline),
// so shaping a link from t=0 gives it no unshaped history: its baseline
// becomes the shaped floor itself, and the RTT band never crosses the
// elevated threshold. Waiting for real unshaped samples first fixes that.
const demoShapedLinkWarmup = 10 * time.Second

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

	var lc api.Controller
	if cfg.Mock {
		log.Printf("mock mode: synthesizing telemetry instead of scraping")
		gen := mock.New(g, st, time.Now().UnixNano())
		go gen.Run(ctx)
		// Preshape one link once the fabric has real unshaped history (see
		// demoShapedLinkWarmup) so the dashboard shows a visibly shaped
		// ("+12MS" chip, elevated band) link without anyone driving the
		// shaping UI. The timer runs for the process lifetime; that's fine
		// here since SetShaping has no ctx-scoped resource to clean up.
		time.AfterFunc(demoShapedLinkWarmup, func() {
			gen.SetShaping(demoShapedLink, &derive.Shaping{DelayMs: f64(12), JitterMs: f64(2)})
			log.Printf("mock: preshaped link %s (+12ms) for the demo", demoShapedLink)
		})
		lc = mock.NewController(g, gen)
	} else {
		targets := scrape.Targets(g)
		interval := time.Duration(cfg.ScrapeIntervalMs) * time.Millisecond
		sc := scrape.New(st, targets, interval, &http.Client{Timeout: scrapeClientTimeout})
		go sc.Run(ctx)
		lc = linkdclient.New(g, &http.Client{Timeout: linkdClientTimeout})
	}

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

// f64 returns a pointer to v, for building derive.Shaping literals.
func f64(v float64) *float64 { return &v }
