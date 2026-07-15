# IETF 126 Vienna Hackathon — SCION Testbed

A 12-AS [SCION](https://scion-architecture.net/) network that runs as
Proxmox LXC containers, built for the SCION Secure Path-Aware Routing
project at the IETF 126 hackathon. Attendees join it as real SCION
endhosts — over WireGuard from their laptop or through a zero-install
browser terminal — and compare path-aware SCION against ordinary BGP/IP
routing on identical links.

## What's in this repo

- **Dashboard** (`dashboard/` — `fabricd` backend + web UI): live topology
  and traffic map, per-link shaping controls (latency/bandwidth/jitter/loss),
  ID-INT path tracing, BGP session badges and a BGP path overlay, plus the
  attendee join flow (WireGuard conf claim, endhost bundles, testbed TLS CA).
- **linkd** (`linkd/`): per-AS link-shaping daemon (`tc` netem/tbf on the
  inter-AS interfaces) with a REST API; also reports BGP sessions/routes.
- **BGP/IP fabric** (`config/AS*/bird.conf`, `ansible/`): BIRD + BFD over
  the same inter-AS links — the "today's Internet" comparison plane, with
  per-AS anchor names (`as150.scion` … `as161.scion`).
- **SCION DNS** (`config/coredns/`): a CoreDNS fork serving the `scion.`
  zone with SVCB `scion=`/`scion-policy=` SvcParams and `scion=` TXT
  records, plus the `scitra` plugin (SCION-IP-translator AAAA synthesis).
- **Attendee access** (`ansible/`, `proxmox/`): WireGuard hub + join page,
  browser playground containers, per-AS endhost bootstrap servers.
- **Topology tooling** (`topology/`): source-of-truth topo files, beacon
  staticinfo metadata sync, and consistency verification.

## Topology

ISD 1, ASes `1-150` … `1-161`: four meshed core ASes (150–153), the rest
non-core beneath them, 24 inter-AS links (including one peering link).
Each AS container runs a border router, control service, and sciond, with
one bridge per inter-AS link. A management network (`10.20.3.0/24`)
carries control/metrics; the BGP fabric uses `10.<AS>.0.0/16` +
`fd00:beef:<AS>::/48` over the same wires. See `topology/topology.topo`
and `config/AS*/topology.json`.

## Testbed layout

The whole thing runs as **22 LXC containers on a single Proxmox host**, wired
together by three kinds of Linux bridge: an isolated **management** network
(`10.20.3.0/24`) for control and metrics, a **public/venue** leg for the
attendee-facing services, and **24 per-link bridges** (`scion1…scion18`) that
each carry one inter-AS link's SCION underlay and BGP fabric.

```mermaid
flowchart TB
    laptop["Attendee laptop<br/>WireGuard tunnel or browser"]

    subgraph host["Proxmox LXC host — 22 containers"]
        direction TB

        subgraph ases["SCION AS nodes · CT150–CT161 · ASes 1-150…1-161 · eth0 = 10.20.3.150–161"]
            asn["each container runs:<br/>border router br1-N-1 · control service cs1-N-1 · sciond<br/>linkd — tc netem/tbf shaping + BGP REST :30480<br/>BIRD — BGP + BFD over the same inter-AS links"]
        end

        subgraph attendee["Attendee access"]
            dash["CT200 · dashboard<br/>fabricd :8080 + web UI"]
            wg["CT201 · wg-hub<br/>WireGuard wg0"]
            play["CT210–CT213 · playground<br/>zero-install browser terminals<br/>homed in AS 158 / 152 / 155 / 161"]
        end

        subgraph svc["Service endhosts · CT214–CT217 · services behind scitra"]
            web["CT215 · web.scion<br/>hev3-server · AS150"]
            web2["CT217 · web2.scion<br/>hev3-server · AS153"]
            wel["CT214 · welcome.scion<br/>nginx · AS151"]
            dns["CT216 · CoreDNS<br/>scion. zone :53 · AS152"]
        end

        mgmt(["mgmt bridge · 10.20.3.0/24<br/>every container eth0 — control-plane + metrics"])
        pub(["public / venue bridge · eth1<br/>globally routable, firewalled to venue prefixes"])
        links(["scion1 … scion18 · 24 inter-AS link bridges<br/>one per link · UDP/50000 underlay · shaped by linkd"])
    end

    laptop -->|WireGuard / HTTPS| pub

    links --- ases
    mgmt --- ases
    mgmt --- attendee
    mgmt --- svc

    pub --- dash
    pub --- wg
    pub --- web
    pub --- web2
    pub --- wel

    dash -. scrapes metrics .-> ases
    play -. SCION via sciond .-> ases
    svc  -. SCION via sciond .-> ases
```

Bridge names differ slightly per host (the venue leg is `vmbr0` on the
mini-PC, `pubnet` on the rack); the roles above are stable. See
`proxmox/create_contianers.sh` for the container/bridge wiring and
`config/AS*/topology.json` for per-link interface and underlay detail.

## Related repositories

- [lschulz/scion](https://github.com/lschulz/scion) — the deployed SCION
  stack (ID-INT + border-router RTT/traffic metrics).
- [tjohn327/dns](https://github.com/tjohn327/dns) and
  [tjohn327/coredns](https://github.com/tjohn327/coredns) (branch
  `scion-dev`) — typed `scion`/`scion-policy` SvcParamKeys and the scitra
  plugin.
- [tjohn327/scion-hev3](https://github.com/tjohn327/scion-hev3) — Happy
  Eyeballs v3 for SCION (racer library, CLI, demo server) and the
  `draft-john-scion-svcb` IETF draft.

## Operating it

- Build, deploy, and rebuild-from-scratch: [DEPLOY.md](DEPLOY.md)
- Booth demo scripts with measured numbers: [DEMOS.md](DEMOS.md)
- Fleet health: `bash tools/booth-check.sh`
- Topology consistency: `python3 topology/verify_topology.py`
- Tests: `cd linkd && make test` · `cd dashboard/backend && go test ./...`
  · `cd dashboard/web && npx vitest run`
