// Package scrape polls the Prometheus /metrics endpoints exposed by each
// AS's border router, control service, and SCION daemon, and writes the
// allowlisted series into the Task-2 ring-buffer Store.
package scrape

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/common/expfmt"

	dto "github.com/prometheus/client_model/go"

	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/store"
	"github.com/netsys-lab/ietf-scion-testbed/dashboard/backend/internal/topo"
)

// Metrics ports, per AS, on the management network (see CLAUDE.md).
const (
	portBR = 30442
	portCS = 30452
	portSD = 30455
)

// Target is one scrape endpoint: a single AS's border router, control
// service, or SCION daemon.
type Target struct {
	AS      int
	Service string // "br" | "cs" | "sd"
	URL     string
}

// Targets builds the three per-AS scrape targets (br, cs, sd) from a
// topology Graph, using each AS's management IP.
func Targets(g topo.Graph) []Target {
	out := make([]Target, 0, len(g.ASes)*3)
	for _, as := range g.ASes {
		out = append(out,
			Target{AS: as.Num, Service: "br", URL: fmt.Sprintf("http://%s:%d/metrics", as.MgmtIP, portBR)},
			Target{AS: as.Num, Service: "cs", URL: fmt.Sprintf("http://%s:%d/metrics", as.MgmtIP, portCS)},
			Target{AS: as.Num, Service: "sd", URL: fmt.Sprintf("http://%s:%d/metrics", as.MgmtIP, portSD)},
		)
	}
	return out
}

// metricKind distinguishes how a Prometheus metric family's value is read.
type metricKind int

const (
	kindGauge metricKind = iota
	kindCounter
)

// rule maps one allowlisted Prometheus metric family to a store key part.
type rule struct {
	family     string     // Prometheus metric family name
	keyPart    string     // store key metric segment
	ifaceLabel string     // label carrying the SCION interface id
	kind       metricKind // gauge or counter
	scaleMs    bool       // multiply value by 1000 (seconds -> milliseconds)
}

// allowlist is the fixed set of metric families the scraper understands.
// Every other family in a target's exposition is ignored. Values are
// summed across all labels except ifaceLabel.
var allowlist = []rule{
	{family: "router_bfd_rtt_estimate_seconds", keyPart: "rtt", ifaceLabel: "interface", kind: kindGauge, scaleMs: true},
	{family: "router_input_bytes_total", keyPart: "input_bytes", ifaceLabel: "interface", kind: kindCounter},
	{family: "router_output_bytes_total", keyPart: "output_bytes", ifaceLabel: "interface", kind: kindCounter},
	{family: "router_input_pkts_total", keyPart: "input_pkts", ifaceLabel: "interface", kind: kindCounter},
	{family: "router_output_pkts_total", keyPart: "output_pkts", ifaceLabel: "interface", kind: kindCounter},
	{family: "router_dropped_pkts_total", keyPart: "dropped_pkts", ifaceLabel: "interface", kind: kindCounter},
	{family: "router_interface_up", keyPart: "up", ifaceLabel: "interface", kind: kindGauge},
	{family: "control_beaconing_received_beacons_total", keyPart: "beacons_recv", ifaceLabel: "ingress_interface", kind: kindCounter},
	{family: "control_beaconing_propagated_beacons_total", keyPart: "beacons_prop", ifaceLabel: "egress_interface", kind: kindCounter},
}

// Scraper polls a fixed set of Targets on a fixed interval, writing samples
// into a Store using the "<as>/<svc>/<metric>/<interface>" key scheme, plus
// a "<as>/<svc>/_up/" health gauge per target.
type Scraper struct {
	st       *store.Store
	targets  []Target
	interval time.Duration
	client   *http.Client
}

// New builds a Scraper. client's Timeout governs the per-request bound
// (the testbed uses 800ms); Run/ScrapeOnce do not impose their own.
func New(st *store.Store, targets []Target, interval time.Duration, client *http.Client) *Scraper {
	return &Scraper{st: st, targets: targets, interval: interval, client: client}
}

// Run starts one goroutine per target, each scraping immediately and then
// on every tick of interval, until ctx is done. Run blocks until ctx is
// done and all target goroutines have returned.
func (s *Scraper) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, t := range s.targets {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			s.scrapeTarget(ctx, t)
			ticker := time.NewTicker(s.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					s.scrapeTarget(ctx, t)
				}
			}
		}(t)
	}
	wg.Wait()
}

// ScrapeOnce performs a single scrape pass over every target and returns
// once all of them have completed. Intended for tests and for populating
// the store immediately at startup.
func (s *Scraper) ScrapeOnce(ctx context.Context) {
	var wg sync.WaitGroup
	for _, t := range s.targets {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			s.scrapeTarget(ctx, t)
		}(t)
	}
	wg.Wait()
}

// scrapeTarget fetches and parses one target's exposition text. On any
// HTTP or parse error it writes a 0 health sample and leaves all other
// series untouched (stale data stays in the ring). On success it writes
// the allowlisted series plus a 1 health sample.
func (s *Scraper) scrapeTarget(ctx context.Context, t Target) {
	now := time.Now().UnixMilli()

	families, err := s.fetch(ctx, t.URL)
	if err != nil {
		s.st.Put(healthKey(t), now, 0)
		return
	}

	for _, rl := range allowlist {
		mf, ok := families[rl.family]
		if !ok {
			continue
		}
		sums := make(map[string]float64)
		for _, m := range mf.GetMetric() {
			ifID := labelValue(m, rl.ifaceLabel)
			if ifID == "" {
				continue
			}
			v := metricValue(m, rl.kind)
			// A NaN or +/-Inf reading (e.g. router_bfd_rtt_estimate_seconds
			// before BFD settles) must never reach the store: one non-finite
			// value poisons derive's view model for the whole link, which
			// makes json.Marshal fail for the entire websocket frame. Drop
			// the sample and keep whatever was previously stored (stale is
			// safer than poisoned).
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			sums[ifID] += v
		}
		for ifID, v := range sums {
			if rl.scaleMs {
				v *= 1000
			}
			key := fmt.Sprintf("%d/%s/%s/%s", t.AS, t.Service, rl.keyPart, ifID)
			s.st.Put(key, now, v)
		}
	}

	s.st.Put(healthKey(t), now, 1)
}

// fetch retrieves and parses a target's Prometheus text exposition.
func (s *Scraper) fetch(ctx context.Context, url string) (map[string]*dto.MetricFamily, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scrape %s: status %d", url, resp.StatusCode)
	}
	var parser expfmt.TextParser
	return parser.TextToMetricFamilies(resp.Body)
}

// healthKey is the "<as>/<svc>/_up/" store key for a target.
func healthKey(t Target) string {
	return fmt.Sprintf("%d/%s/_up/", t.AS, t.Service)
}

// labelValue returns the value of label name on m, or "" if absent.
func labelValue(m *dto.Metric, name string) string {
	for _, lp := range m.GetLabel() {
		if lp.GetName() == name {
			return lp.GetValue()
		}
	}
	return ""
}

// metricValue reads m's value according to kind.
func metricValue(m *dto.Metric, kind metricKind) float64 {
	switch kind {
	case kindGauge:
		return m.GetGauge().GetValue()
	case kindCounter:
		return m.GetCounter().GetValue()
	default:
		return 0
	}
}
