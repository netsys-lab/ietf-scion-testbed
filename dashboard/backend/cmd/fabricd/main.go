// Command fabricd is the SCION Fabrik dashboard backend. It builds the
// testbed's topology graph, feeds a ring-buffer Store from either the real
// Prometheus scrapers or (in mock mode) a synthetic generator, derives
// link/AS view models, and serves the dashboard's REST + WebSocket API and
// static frontend.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
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

// baselinesSaveInterval is how often the running RTT baselines are flushed
// to baselines_path, when configured.
const baselinesSaveInterval = 60 * time.Second

// config is fabricd's on-disk configuration.
type config struct {
	Listen           string `toml:"listen"`
	ConfigDir        string `toml:"config_dir"`
	StaticDir        string `toml:"static_dir"`
	ScrapeIntervalMs int    `toml:"scrape_interval_ms"`
	Mock             bool   `toml:"mock"`

	// BaselinesPath, if non-empty, is a JSON file (map[string]float64) used
	// to persist derive.Deriver's per-link running-minimum RTT baselines
	// (internal/derive.(*Deriver).Baselines / SeedBaselines) across fabricd
	// restarts. It is loaded once at startup if present, then saved
	// atomically every baselinesSaveInterval and once on SIGINT/SIGTERM.
	// Default "" disables persistence entirely -- baselines are derived
	// fresh from the store's ring on every restart, exactly as before this
	// field existed.
	//
	// Operational note: if the topology's real link characteristics
	// legitimately change (e.g. a topology/staticinfo.yml redeploy that
	// changes a link's underlying delay), this file must be deleted by hand
	// before restarting fabricd. Otherwise the stale, now-too-low persisted
	// minimum keeps classifying the new, legitimately-higher RTT as
	// shaped/elevated forever -- fabricd has no way to tell "real
	// topology change" apart from "still shaped" on its own.
	BaselinesPath string `toml:"baselines_path"`

	// The fields below configure the attendee join flow (Plan B): JoinEnabled
	// gates the whole /api/join + /api/instructions surface (see
	// api.JoinConfig's doc comment -- disabled must make those routes 404 as
	// if they didn't exist); the rest describe the booth code, joinable ASes,
	// the WireGuard hub this fabricd offers attendees into, and where the
	// pool file / instructions content / playground proxy targets live.
	JoinEnabled     bool              `toml:"join_enabled"`
	BoothCode       string            `toml:"booth_code"`
	ISD             int               `toml:"isd"`
	JoinableASes    []int             `toml:"joinable_ases"`
	WGPoolPath      string            `toml:"wg_pool_path"`
	WGStatePath     string            `toml:"wg_state_path"`
	InstructionsDir string            `toml:"instructions_dir"`
	EndpointV6      string            `toml:"endpoint_v6"`
	EndpointV4      string            `toml:"endpoint_v4"`
	WGListenPort    int               `toml:"wg_listen_port"`
	HubProbeAddr    string            `toml:"hub_probe_addr"`
	PlayProxy       map[string]string `toml:"play_proxy"`
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
		ISD:              1,
		WGListenPort:     51820,
		HubProbeAddr:     "10.20.3.201:22",
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

	playTargets := make(map[int]string, len(cfg.PlayProxy))
	for k, v := range cfg.PlayProxy {
		n, err := strconv.Atoi(k)
		if err != nil {
			log.Fatalf("play_proxy: bad AS key %q: %v", k, err)
		}
		playTargets[n] = v
	}
	jc := api.JoinConfig{
		Enabled:         cfg.JoinEnabled,
		BoothCode:       cfg.BoothCode,
		ISD:             cfg.ISD,
		JoinableASes:    cfg.JoinableASes,
		ConfigDir:       cfg.ConfigDir,
		InstructionsDir: cfg.InstructionsDir,
		EndpointV6:      cfg.EndpointV6,
		EndpointV4:      cfg.EndpointV4,
		ListenPort:      cfg.WGListenPort,
		HubProbeAddr:    cfg.HubProbeAddr,
		PlayTargets:     playTargets,
		RateMax:         5,
		RateWindow:      time.Minute,
	}
	var pool api.PoolStore // nil until B3 wires a real wgpool-backed store.

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
		// The join flow's WG hub, pool file, and playground targets are all
		// real testbed infrastructure that mock mode has none of; force it
		// off regardless of what config.toml says, so a mock/demo run never
		// advertises a join surface it can't actually serve.
		jc.Enabled = false
	} else {
		targets := scrape.Targets(g)
		interval := time.Duration(cfg.ScrapeIntervalMs) * time.Millisecond
		sc := scrape.New(st, targets, interval, &http.Client{Timeout: scrapeClientTimeout})
		go sc.Run(ctx)
		lc = linkdclient.New(g, &http.Client{Timeout: linkdClientTimeout})
	}

	d := derive.New(g, st)
	if cfg.BaselinesPath != "" {
		go runBaselinePersistence(cfg.BaselinesPath, d)
	}

	var static fs.FS
	if info, err := os.Stat(cfg.StaticDir); err == nil && info.IsDir() {
		static = os.DirFS(cfg.StaticDir)
	} else {
		log.Printf("warning: static dir %q not found; static file serving disabled", cfg.StaticDir)
	}

	h := api.New(g, st, d, lc, static, jc, pool)
	go api.RunBroadcast(ctx, h, frameInterval, pollInterval)

	log.Printf("fabricd listening on %s", cfg.Listen)
	log.Fatal(http.ListenAndServe(cfg.Listen, h))
}

// f64 returns a pointer to v, for building derive.Shaping literals.
func f64(v float64) *float64 { return &v }

// loadBaselines reads the JSON map[string]float64 at path, for the
// baselines_path config key. A missing file is not an error -- it just
// means there is nothing to seed yet (first run, or the operator deleted it
// per the reset note on the config key) -- and returns (nil, nil).
func loadBaselines(path string) (map[string]float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m map[string]float64
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// saveBaselines atomically writes m as JSON to path: it writes to a
// same-directory temp file and renames it over path, so a concurrent reader
// or a crash mid-write never observes a partial file.
func saveBaselines(path string, m map[string]float64) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// runBaselinePersistence loads baselines_path into d at startup if present,
// then saves d's current baselines to it every baselinesSaveInterval for the
// life of the process. It also arms a SIGINT/SIGTERM handler that saves once
// more before the process exits, so a normal `systemctl stop`/Ctrl-C does
// not lose up to baselinesSaveInterval of baseline history. Call only when
// cfg.BaselinesPath is non-empty.
//
// There is no ctx-cancellation exit path: main runs until
// log.Fatal(http.ListenAndServe(...)), which calls os.Exit and skips
// deferred cancel(), so this goroutine only ever ends via the signal
// handler's os.Exit(0) above.
func runBaselinePersistence(path string, d *derive.Deriver) {
	if m, err := loadBaselines(path); err != nil {
		log.Printf("warning: baselines_path %q: %v (starting cold)", path, err)
	} else if len(m) > 0 {
		d.SeedBaselines(m)
		log.Printf("seeded %d RTT baselines from %s", len(m), path)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(baselinesSaveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := saveBaselines(path, d.Baselines()); err != nil {
				log.Printf("warning: saving baselines to %s: %v", path, err)
			}
		case <-sigCh:
			if err := saveBaselines(path, d.Baselines()); err != nil {
				log.Printf("warning: saving baselines to %s on shutdown: %v", path, err)
			}
			signal.Stop(sigCh)
			os.Exit(0)
		}
	}
}
