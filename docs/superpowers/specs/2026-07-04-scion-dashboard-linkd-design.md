# SCION Testbed Dashboard + Link-Shaping Daemon — Design

Date: 2026-07-04 · Branch: `feat/dashboard` · Status: approved by Tony (sections 1–3)

## Goal

Two deliverables for the IETF 126 Vienna hackathon testbed:

1. **`scion-linkd`** — a daemon in every AS container that shapes the inter-AS
   links (latency, jitter, packet loss, bandwidth) with `tc`/netem via a REST
   API. Shipped as a Debian package, runs as a systemd service.
2. **Dashboard ("scion-fabric")** — a live web UI showing the 12-AS topology
   with per-link RTT, traffic, loss, and health, fed by the Prometheus metrics
   of the modified SCION stack (`/home/tony/lshulz/scion`, branch
   `idint2026`), with sliders that drive `scion-linkd`.

**Demo story**: drag a slider → the network actually degrades (netem) → the
dashboard shows RTT/loss/throughput change within seconds → SCION
path-awareness reacts. Big-screen booth mode + hands-on laptop mode.

Decisions taken with Tony: big-screen + hands-on audience; topology first,
path/ID-INT view as stretch; dashboard stack in a dedicated LXC on the
management net; custom Go backend + React UI (no Prometheus/Grafana
dependency); linkd in Go using netlink.

## Key metrics consumed (from the lshulz fork)

Border router (`10.20.3.15x:30442/metrics`), labels include `interface`
(ifid) and `neighbor_isd_as`:

- `router_bfd_rtt_estimate_seconds` (gauge) — live per-link RTT from BFD poll
  sequences (fork addition; EWMA-smoothed). Primary edge-color signal.
- `router_input_bytes_total` / `router_output_bytes_total`,
  `router_input_pkts_total` / `router_output_pkts_total` (counters, output
  with traffic-`type` label) — per-direction throughput.
- `router_dropped_pkts_total` (counter, `reason` label) — drops.
- `router_interface_up` (gauge), `router_bfd_state_changes_total` — link
  liveness.

Control service (`:30452`): `control_beaconing_{originated,propagated,
received}_beacons_total` per interface — control-plane activity layer.
SCION daemon (`:30455`): path request counters — per-AS demand indicator.
Scrape success/failure itself feeds the per-AS service-health LEDs.

## Component 1 — `scion-linkd`

Go static binary, systemd service (`CAP_NET_ADMIN`), Debian package.

**Interface discovery.** At startup parse `/etc/scion/AS*/topology.json`
(glob configurable). For each BR interface, take the underlay `local` IPv6
address and resolve the owning netdev via netlink address lookup. Interface
naming conventions (`sci<X>`) are never trusted; `eth0`/`eth1` can never be
selected because they carry no underlay address.

**Shaping.** One netem qdisc per netdev, applied with `QdiscReplace`
(github.com/vishvananda/netlink): `delay` + `jitter`, `loss`, and `rate`
(netem `Rate64`). Egress shaping ⇒ each link direction is controlled at its
sending BR. No state file: the kernel's qdisc state is the single source of
truth; `GET` reads it back, restarts need no reconciliation.

**REST API** (binds the management IP, port `30480`):

| Method/path | Behavior |
|---|---|
| `GET /api/v1/links` | All shapeable interfaces: ifid, neighbor ISD-AS, link type, netdev, currently applied `{delay_ms, jitter_ms, loss_pct, rate_mbit}` (absent field = unshaped) |
| `PUT /api/v1/links/{ifid}` | Apply any subset of the four params; unspecified params keep current values; atomic qdisc replace |
| `DELETE /api/v1/links/{ifid}` | Remove netem entirely (reset to unshaped) |
| `GET /healthz` | Liveness + count of managed interfaces |

Validation hard-caps: delay 0–2000 ms, jitter 0–1000 ms (jitter requires
delay > 0), loss 0–100 %, rate 0.1–1000 Mbit. Requests outside caps → 400.

**Packaging.** `linkd/` Go module; `make deb` builds with `dpkg-deb` (no
extra tooling): binary → `/usr/bin/scion-linkd`, unit →
`/lib/systemd/system/scion-linkd.service`, default config →
`/etc/scion-linkd/config.toml` (listen addr, topology glob), `postinst`
enables+starts. Installed on all 12 AS containers via a new Ansible playbook.

## Component 2 — Dashboard backend

One Go binary on the dashboard LXC. Modules:

- **Topology model** — built from `config/AS*/topology.json` + `ifids.yml`
  (synced to the LXC; path in config). Canonical graph: 12 AS nodes, 24
  links; a link = (BR-A ifid, BR-B ifid) + subnet + type (core/child/peer).
  Convention: endpoint A is the lower AS number; `a_to_b` always means
  "A's egress toward B".
- **Scraper** — 36 Prometheus targets (12 × BR/CS/SD), 1 s interval, 800 ms
  timeout, `prometheus/common/expfmt` parsing. Scrape failure ⇒ target
  marked stale (displayed as service-health LED). linkd is not scraped here;
  its REST API is polled by the derive module (below).
- **Store** — per-series ring buffers, 3600 samples (1 h @ 1 s), a few MB
  total. Counter-reset-aware rate computation.
- **Derive** — per-link view models: RTT per side (both BRs report it; they
  may differ), per-direction throughput/pkt rates, drop rates by reason,
  wire-loss estimate (A egress rate − B ingress rate on the same link, which
  visualizes netem loss independently of linkd), up/down state, beaconing
  rates, current shaping (polled from linkd every 5 s + immediately after a
  control action).
- **API** —
  - `GET /api/topology` — graph + fixed layout coordinates.
  - `WS /api/live` — full snapshot on connect, then 1 s JSON delta frames;
    reconnect ⇒ new snapshot; no server-side session state.
  - `GET /api/history?series=…&mins=…` — sparkline backfill.
  - `PUT /api/links/{linkId}/shaping` — body has params +
    `direction: a_to_b | b_to_a | both`; fans out to one/two linkd PUTs;
    per-endpoint results returned, partial failure reported.
  - `POST /api/links/{linkId}/reset`; `GET /api/health` (scrape/linkd
    reachability matrix).
- **Static file serving** for the built frontend. Deployment = one binary +
  `dist/` + YAML config; systemd unit; packaged as a deb too.
- **Mock mode** (`--mock`) — same APIs from a synthetic generator (RTT
  random-walks, varying traffic, scriptable link failures) so frontend dev
  and booth rehearsal don't need the testbed.

## Component 3 — Frontend

Vite + React + TypeScript. SVG for structure, canvas overlay (rAF) for
particle animation; d3 for scales/paths only. Fixed layered layout as JSON
(core 150–153 top, 154–157 mid, 158–161 leaf, AS155 central), seeded from
`topology/topology.drawio`.

Visual encodings (normative):

| Signal | Encoding |
|---|---|
| Configured capacity (linkd rate; nominal 100 Mbit when unshaped) | Edge stroke width |
| Throughput per direction | Animated particle stream density/speed |
| RTT (`router_bfd_rtt_estimate_seconds`) | Edge color gradient teal→amber→red |
| Loss (netem + wire-loss estimate) | Particles pop/fade mid-link |
| Link down (`router_interface_up`/BFD) | Dark edge + break mark |
| Active shaping | Chip on edge ("50 Mbit · +20 ms · 1 %") |
| BR/CS/SD scrape health | Three LEDs per AS node |
| Core AS | Node badge; core tier visually emphasized |

Views: **Fabric** (main) with KPI strip (total pkts/s, links up, avg core
RTT, beacons/s) + event ticker (RTT jumps, state changes, shaping actions);
**link side-panel** (sparklines, four sliders, direction toggle,
apply/reset); **AS side-panel** (per-interface table, beaconing, sciond
demand). `?mode=screen` = big-screen: larger type, no panels, auto-fit.
`/paths` route stubbed for the stretch path/ID-INT probe view. Dark theme
default; implementation will follow the dataviz + frontend-design skills.

## Repo layout

```
linkd/                    Go module: daemon + debian packaging + Makefile
dashboard/backend/        Go module
dashboard/web/            Vite + React frontend
ansible/playbooks/        deploy-linkd.yaml, deploy-dashboard.yaml
docs/superpowers/specs/   this document
```

## Error handling summary

- Scrape failure → stale marker, LED off; series gap, no fake zeros.
- linkd unreachable → shaping chips greyed, control returns explicit
  per-endpoint error; dashboard never blocks on linkd.
- WS drop → client auto-reconnect, snapshot re-sync.
- Counter resets (service restart) → rate clamps to ≥ 0, no spikes.
- linkd validation rejects out-of-range params (400); netem apply errors
  are returned verbatim to the dashboard and shown in the ticker.

## Testing

- **linkd**: unit tests (topology→netdev mapping, validation); integration
  script using network namespaces + veth pairs asserting `tc` state; runs on
  any Linux dev box.
- **backend**: fixture tests — recorded `/metrics` dumps → derive → expected
  view models; counter-reset cases; WS protocol snapshot/delta test.
- **frontend**: TypeScript strictness + smoke test; visual work against
  `--mock`.
- **Rehearsal**: mock mode end-to-end + one live AS container when deployed.

## Deployment

New LXC **200** (`10.20.3.200`) with legs on `vmbr0` (mgmt) and `pubnet`
(venue access), added to `proxmox/create_contianers.sh` and the Ansible
inventory. Both debs installed via the new playbooks.

## Prerequisite fixes (testbed bugs found 2026-07-04)

The dashboard needs a healthy fabric; these go in as separate commits first:

1. 150↔154 underlay mismatch: AS150 side on `fd00:fade:4::` (collides with
   151↔152), AS154 expects `fd00:fade:7::`.
2. 151↔153 underlay mismatch: AS151 side on `fd00:fade:6::` (collides with
   152↔153), AS153 expects `fd00:fade:5::`.
3. `config/AS151/sd.toml` prometheus addr typo `10.20.3.1519`.
4. `config/AS160/*.toml` prometheus addrs point at `10.20.3.161`.
5. `proxmox/create_contianers.sh` bridge/IP assignments for AS151/152/153/
   156/157 out of sync with `config/` (sci2/sci5/sciA/sciC/sciD tangle).

## Out of scope (this hackathon)

Auth on mgmt-net APIs; durable metric history beyond 1 h; Prometheus/Grafana
integration; path/ID-INT probe view is stretch-only (route stub, no
implementation commitment).
