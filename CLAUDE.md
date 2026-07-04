# IETF 126 Vienna Hackathon — SCION Testbed

SCION testbed for the SCION Secure Path-Aware Routing project at the IETF 126
hackathon (see `local-docs/hackathon-wiiki.md`). Runs a 12-AS SCION network as
Proxmox LXC containers. Hackathon deliverables built in this repo:

1. **Dashboard** — live topology + traffic visualization, fed by Prometheus
   metrics scraped from SCION services over the management network, with
   controls to change link properties.
2. **Link-shaping daemon** — runs in every AS container as a systemd service
   (shipped as a Debian package), applies `tc` (netem/tbf) to the inter-AS
   interfaces to set latency, bandwidth, jitter, and packet loss. Controlled
   from the dashboard.

## Topology

- ISD 1, ASes `1-150` … `1-161`. Core ASes: 150–153 (meshed); the rest are
  non-core below them. AS155 is the highest-degree hub (8 links). One border
  router (`br1-<AS>-1`), one control service (`cs1-<AS>-1`), and one sciond
  per AS. 24 inter-AS links total incl. one peering link (155↔158).
- Source of truth for links/ifids: `config/AS*/topology.json` (per-AS) and
  `config/ifids.yml` (ifid ↔ remote ifid map). Human-readable topology:
  `topology/topology.topo` (+ drawio/svg). Core list: `config/as_list.yml`.

## Networks & addressing

- **Management**: bridge `vmbr0`, `10.20.3.0/24`. Container for AS15x has
  `eth0` = `10.20.3.15x` (Kea DHCP, MAC-pinned; server container at
  10.20.3.1/.190). All SCION control/metrics endpoints bind this network.
- **Public**: `eth1` on bridge `pubnet` (IETF IPv4+IPv6 network).
- **Inter-AS links**: one Proxmox bridge per link, `scion<X>` with hex X;
  in-container interface `sci<X>` with IPv6 `fd00:fade:<X>::<ASnum>/64`,
  underlay UDP port 50000. These `sci*` interfaces are what the TC daemon
  shapes. Map ifid → interface via the underlay `local` address in
  `topology.json`, never by interface-name convention.

## Metrics endpoints (per AS, on 10.20.3.15x)

| Service | Port | Config |
|---|---|---|
| Border router | 30442 | `config/AS*/br1-*.toml` `[metrics] prometheus` |
| Control service | 30452 | `config/AS*/cs1-*.toml` |
| SCION daemon | 30455 | `config/AS*/sd.toml` |

All services run with `experimental_idint = true`. The deployed SCION stack is
the **modified fork at `/home/tony/lshulz/scion`** (adds ID-INT support and
RTT + per-interface traffic metrics to the border router). Check that repo,
not upstream scionproto, when reasoning about available metrics.

## Deployment

- Containers: `proxmox/create_contianers.sh` (`pct create`; note the filename
  typo is intentional/existing — don't "fix" references casually).
- Provisioning: `ansible/` (inventory: AS containers as `AS1-15x`,
  `ansible_user: ietf`).
- Service configs land in `/etc/scion/AS<num>/` inside containers.

## Known issues (as of 2026-07-04 — verify before relying on topology data)

- Underlay subnet mismatches that break links as configured:
  - 150↔154: AS150 side uses `fd00:fade:4::` (collides with 151↔152),
    AS154 side expects `fd00:fade:7::`.
  - 151↔153: AS151 side uses `fd00:fade:6::` (collides with 152↔153),
    AS153 side expects `fd00:fade:5::`.
  - `create_contianers.sh` disagrees with `config/` for AS151/152/153/156/157
    (sci5/sciA/sciC/sciD tangles); configs are authoritative.
- `config/AS151/sd.toml`: prometheus addr typo `10.20.3.1519`.
- `config/AS160/*.toml`: prometheus addrs point at `10.20.3.161` (AS161's IP)
  — will fail to bind on the AS160 container.

## Working rules

- Work on the feature branch (`feat/dashboard`), never commit to `main`.
- Commits: **no Claude author/co-author** — plain commits under the user's git
  identity, no `Co-Authored-By` / `Generated with` trailers. The user pushes;
  never push.
- Model routing for subagents: **Haiku** for reading/exploration; **Sonnet 5**
  for straightforward implementation; **Opus** for planning, judging, review,
  and advanced implementation; **Fable 5** for brainstorming and dashboard
  design.
