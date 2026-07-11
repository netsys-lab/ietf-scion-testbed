// Command hev3 is the SCION-aware Happy Eyeballs v3 CLI: it fetches one
// https:// URL, racing SCION/IPv6/IPv4 candidates per
// draft-ietf-happy-happyeyeballs-v3-04 (extended with SCION — see
// docs/superpowers/specs/2026-07-10-scion-svcb-hev3-design.md), and prints
// either a human race table + response preview or a JSON summary.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/netsys-lab/ietf-scion-testbed/hev3/pkg/hev3"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("hev3", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: hev3 [flags] URL")
		fs.PrintDefaults()
	}

	resolver := fs.String("resolver", "", "DNS resolver \"ip:port\"; empty = system resolver (/etc/resolv.conf)")
	k := fs.Int("k", 0, "ranked SCION paths kept per SCION candidate (0 = library default)")
	noSCION := fs.Bool("no-scion", false, "drop SCION candidates after resolve")
	noIP := fs.Bool("no-ip", false, "drop IPv6/IPv4 candidates after resolve")
	jsonOut := fs.Bool("json", false, "emit a JSON summary instead of the human race table")
	insecure := fs.Bool("insecure", false, "skip TLS certificate verification on every leg")
	ca := fs.String("ca", "", "PEM root CA file (default: /etc/hev3/ca.pem if it exists)")
	attemptDelay := fs.Duration("attempt-delay", 0, "race attempt stagger (0 = library default, 250ms)")
	resolutionDelay := fs.Duration("resolution-delay", 0, "SVCB resolution delay gate (0 = library default, 50ms)")
	timeout := fs.Duration("timeout", 15*time.Second, "overall fetch timeout")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "hev3: expected exactly one URL argument")
		fs.Usage()
		return 2
	}
	rawURL := fs.Arg(0)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	res, err := hev3.Fetch(ctx, rawURL, hev3.Options{
		Resolver:        *resolver,
		K:               *k,
		NoSCION:         *noSCION,
		NoIP:            *noIP,
		Insecure:        *insecure,
		CAFile:          *ca,
		AttemptDelay:    *attemptDelay,
		ResolutionDelay: *resolutionDelay,
	})
	if err != nil {
		fmt.Fprintln(stderr, "hev3:", err)
		return 1
	}

	if *jsonOut {
		printJSON(stdout, res)
		return 0
	}
	printHuman(stdout, res)
	return 0
}

// --- human output ---------------------------------------------------------

// raceRow is one line of the printed race table, derived entirely from
// Timeline events (no side-channel bookkeeping): a candidate's Label first
// seen via a "candidate" event with no later "attempt" is "never-started";
// otherwise its outcome/timing come from "attempt" plus whichever of
// "success"/"fail"/"cancel" (and "winner") follows.
type raceRow struct {
	label     string
	started   bool
	startMs   int64
	outcome   string // won / failed / cancelled; "" until one of those events fires
	outcomeMs int64
	winner    bool
	detail    string
}

func buildRaceRows(events []hev3.Event) []raceRow {
	index := map[string]int{}
	var rows []raceRow
	row := func(label string) *raceRow {
		if i, ok := index[label]; ok {
			return &rows[i]
		}
		rows = append(rows, raceRow{label: label})
		index[label] = len(rows) - 1
		return &rows[len(rows)-1]
	}

	for _, ev := range events {
		switch ev.Kind {
		case "candidate":
			row(ev.Label)
		case "attempt":
			r := row(ev.Label)
			r.started = true
			r.startMs = ev.At.Milliseconds()
		case "success":
			r := row(ev.Label)
			r.outcome = "won"
			r.outcomeMs = ev.At.Milliseconds()
		case "winner":
			row(ev.Label).winner = true
		case "fail":
			r := row(ev.Label)
			r.outcome = "failed"
			r.outcomeMs = ev.At.Milliseconds()
			r.detail = ev.Detail
		case "cancel":
			r := row(ev.Label)
			r.outcome = "cancelled"
			r.outcomeMs = ev.At.Milliseconds()
		}
	}
	return rows
}

// familyFromLabel infers a Candidate's family from its Label prefix — the
// only place that survives into Timeline events. It mirrors the label
// conventions resolver.go/dial_scion.go actually use ("scion:", "scitra:",
// "v6:"/"v6hint:", "v4:"/"v4hint:", with per-path SCION expansion appending
// "#pN" to a "scion:" prefix).
func familyFromLabel(label string) string {
	switch {
	case strings.HasPrefix(label, "scitra:"):
		return "SCION(scitra)"
	case strings.HasPrefix(label, "scion:"):
		return "SCION"
	case strings.HasPrefix(label, "v6"):
		return "IPv6"
	case strings.HasPrefix(label, "v4"):
		return "IPv4"
	default:
		return "?"
	}
}

func familyName(f hev3.Family) string {
	switch f {
	case hev3.FamilySCION:
		return "SCION"
	case hev3.FamilyIPv6:
		return "IPv6"
	case hev3.FamilyIPv4:
		return "IPv4"
	default:
		return "?"
	}
}

func printHuman(w *os.File, res *hev3.Result) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "LABEL\tFAMILY\tSTART(ms)\tOUTCOME\tWINNER")
	for _, r := range buildRaceRows(res.Timeline) {
		start := "-"
		if r.started {
			start = fmt.Sprintf("%d", r.startMs)
		}
		var outcome string
		switch {
		case r.outcome != "":
			outcome = fmt.Sprintf("%s %dms", r.outcome, r.outcomeMs)
		case r.started:
			// Reachable only if Race returned a winner while another
			// attempt's fail/cancel event has not yet been observed —
			// shouldn't happen given Race's synchronous event ordering,
			// but printed honestly rather than mislabeled "never-started".
			outcome = "in-flight"
		default:
			outcome = "never-started"
		}
		mark := ""
		if r.winner {
			mark = "<-- WINNER"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.label, familyFromLabel(r.label), start, outcome, mark)
	}
	tw.Flush()

	fmt.Fprintln(w)
	fmt.Fprintf(w, "winner: %s (%s, alpn=%s)\n", res.Winner.Label, familyName(res.Winner.Family), res.ALPN)
	fmt.Fprintln(w, res.Status)
	fmt.Fprintln(w)
	printBodyPreview(w, res.Body)
}

// printBodyPreview prints up to the first 20 lines of body (already capped
// at 4KiB by Fetch).
func printBodyPreview(w *os.File, body []byte) {
	const maxLines = 20
	lines := strings.SplitAfter(string(body), "\n")
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	for _, l := range lines {
		fmt.Fprint(w, l)
	}
	if len(lines) > 0 && !strings.HasSuffix(lines[len(lines)-1], "\n") {
		fmt.Fprintln(w)
	}
}

// --- JSON output -----------------------------------------------------------

type jsonWinner struct {
	Label  string `json:"label"`
	Family string `json:"family"`
}

type jsonEvent struct {
	AtMs   int64  `json:"at_ms"`
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
}

type jsonResult struct {
	Winner   jsonWinner  `json:"winner"`
	ALPN     string      `json:"alpn"`
	Status   string      `json:"status"`
	Timeline []jsonEvent `json:"timeline"`
}

func printJSON(w *os.File, res *hev3.Result) {
	out := jsonResult{
		Winner: jsonWinner{
			Label:  res.Winner.Label,
			Family: familyName(res.Winner.Family),
		},
		ALPN:   res.ALPN,
		Status: res.Status,
	}
	for _, ev := range res.Timeline {
		out.Timeline = append(out.Timeline, jsonEvent{
			AtMs:   ev.At.Milliseconds(),
			Kind:   ev.Kind,
			Label:  ev.Label,
			Detail: ev.Detail,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}
