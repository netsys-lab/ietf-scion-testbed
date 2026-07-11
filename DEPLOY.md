# Deploy runbook

Build and deploy the SCION in a Box dashboard (fabricd + web) and the
link-shaping daemon (scion-linkd) to the IETF 126 testbed.

## Host network

Containers run **Ubuntu 24.04 LTS** (glibc 2.39 — the SCION reference build
platform). The template is `ubuntu-24.04-standard_24.04-2` (`pveam download
local ...`); `proxmox/create_contianers.sh` defaults to it.

The Proxmox host's testbed bridges are defined canonically in
`proxmox/interfaces.d-scion-testbed`, installed to
`/etc/network/interfaces.d/scion-testbed` and applied with `ifreload -a`. It
defines the isolated `mgmt` bridge (host `10.20.3.1/24`, `bridge-ports none`)
that carries the internal management plane `10.20.3.0/24` and NATs it out via
the venue uplink (`vmbr0`), plus the 24 `scion*` inter-AS link bridges.
Containers attach `eth0` to `mgmt` with static `10.20.3.<id>` addresses (see
`proxmox/create_contianers.sh`); only the dashboard (CT200) and wg-hub
(CT201) also carry a venue leg (`eth1` on `vmbr0`).

`create_contianers.sh` now handles the host-level container prerequisites that
were previously manual, so a clean rebuild is one command:
- **`--rootfs local-lvm:N`** — the only storage here that supports container
  rootdir (pct defaults to the `local` dir storage and fails without it).
- **`--features nesting=1`** on every container — Ubuntu 24.04's systemd 255
  warns/misbehaves in an LXC without it ("Systemd 255 detected…").
- **`/dev/net/tun` passthrough** for `scitra-tun` (CT210–217: playground
  210–213 + svc endhosts 214–217): loads the `tun` module (persisted to
  `/etc/modules-load.d/`) and appends
  `lxc.cgroup2.devices.allow: c 10:200 rwm` +
  `lxc.mount.entry: /dev/net/tun dev/net/tun none bind,create=file` to each
  `/etc/pve/lxc/<id>.conf`, then reboots the container.
- **SSH keys** — installs `proxmox/public_keys` (host ansible key + dev infra
  key) for root, the entry point for the `ietf` bootstrap below.

**CPU weights.** Containers carry tiered `cpuunits` (cgroup-v2 cpu.weight;
set by `create_contianers.sh`, live-applied with `pct set`): AS containers
1000 (the BR dataplane must win contention), dashboard 300, wg-hub 200,
playground/svc 50 — a 20:1 BR-vs-guest ratio, work-conserving when idle.
Inside each playground CT, attendee shells additionally live in
`guest.slice` capped at `CPUQuota=80%`/`CPUWeight=20`
(`/etc/systemd/system/guest.slice.d/cpu.conf`, shipped by
`deploy_playground.yaml`) so a busy guest cannot starve that container's
own scitra/sciond/ttyd. Caveat: WireGuard crypto and bridge/veth forwarding
run in kernel context outside any container cgroup — the link bandwidth
tiers are the bound there, not these weights.

## Prereqs

- Go 1.22+, Node 22+, `dpkg-deb` (build host).
- `ansible` on the management host, with SSH access to the AS containers and
  the dashboard container per `ansible/inventory.yaml`.
- python3 + PyYAML (`apt install python3-yaml` or `pip install pyyaml`) — the
  first Build-order command below imports `yaml` and dies on a fresh host
  without it.

## Build order

Check the beacon metadata is in sync before building anything else — the
generator (`topology/gen_staticinfo.py`) is the source of the committed
`config/AS*/staticInfoConfig.base.json` and `linkd-baseline.json` files, and
drift here means linkd and the CS would ship stale metadata:

```sh
python3 topology/gen_staticinfo.py --check   # expect: OK: 12 ASes generated, files match
```

Build the frontend before the backend deb — `fabricd`'s `make deb` bundles
`dashboard/web/dist` into the package and **fails if it's missing** (set
`SKIP_WEB=1` to intentionally build a headless deb, e.g. for backend-only
iteration).

```sh
cd dashboard/web && npm ci && npm run build
cd ../backend && make deb
cd ../../linkd && make deb
SCION_FORK=${SCION_FORK:-/home/tony/lshulz/scion}
(cd "$SCION_FORK" && go build -o bin/control ./control/cmd/control)
```

Both `make deb` targets produce `dist/*_amd64.deb` (`linkd` ships
`scion-linkd_0.2.0_amd64.deb`, with the beacon-metadata config keys and CS
reload support). The last command builds the patched control-service binary
(staticinfo SIGHUP reload) from the fork at `$SCION_FORK` — override the env
var if your checkout lives somewhere other than the default below.

Build the attendee endhost binaries (used by the Tier 1 playground
containers) natively from the same fork:

```sh
./tools/build-endhost.sh   # -> .build/endhost/bin/{scion,sciond,shim-dispatcher}
```

### Fork provenance

The patched control service is built from a fork, not upstream scionproto:

- Fork remote: `https://github.com/lschulz/scion` (public).
- Branch: `ietf-126`.
- Commit: `158d2060b`, based on upstream `8ce7ed2f8` (the commit pinned for
  this deploy).
- Local build checkout: `$SCION_FORK`, default `/home/tony/lshulz/scion`
  (already tracks `origin/ietf-126`).

**This branch is pushed** to `github.com/lschulz/scion` (branch `ietf-126`),
so a fresh checkout or a different build host can reproduce the
reload-capable CS and the endhost binaries:

```sh
git clone -b ietf-126 https://github.com/lschulz/scion "$SCION_FORK"
```

The CS binary running on the testbed and the attendee endhost binaries
(`tools/build-endhost.sh`) are both built from this commit.

## Deploy

One-time, from a fresh management host, before anything else in this
section: accept the SSH host keys of every inventory host, so ansible
doesn't stall on interactive host-key prompts (or fail outright in a
non-interactive context):

```sh
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/localhost_accept_host_keys.yaml
```

(After a fresh rebuild the containers have new SSH host keys; clear the stale
ones first — `for ip in 150..161 200 201 210..217; do ssh-keygen -R 10.20.3.$ip; done`
— then re-scan, or ansible fails with "REMOTE HOST IDENTIFICATION HAS CHANGED".)

**Bootstrap the `ietf` deploy user (first thing after `create_contianers.sh`).**
Fresh containers only have root SSH; every other playbook connects as `ietf`,
which does not exist yet. The inventory's `ansible_user: ietf` overrides `-u`,
so pass root as an **extra-var**:

```sh
ansible-playbook -i ansible/inventory.yaml -e ansible_user=root \
  ansible/playbooks/bootstrap_ietf_user.yaml
```

One-time, before the first deploy (or whenever the CS unit/binary naming on
the containers is in doubt): discover the actual systemd unit name and binary
path for the control service. The CS binary path is identical across ASes, so
a single host's answer applies to the whole `ases` group — run this against
`AS1-150` only:

```sh
ansible -i ansible/inventory.yaml AS1-150 -b \
  -m shell -a 'systemctl list-units "*scion*" --no-legend; readlink /proc/$(pgrep -o -f cs1-150)/exe'
```

Record the results in `ansible/inventory.yaml` (or a `group_vars/ases.yml`)
as three vars on the `ases` group — `ansible/group_vars/ases.yml.example`
has the exact keys pre-filled with placeholders; copy it to
`ansible/group_vars/ases.yml` (dropping the `.example` suffix, which is
what keeps ansible from auto-loading it as-is) and fill in your discovered
values:

- `linkd_cs_unit` — the CS systemd unit name (e.g. `scion-control@AS150`,
  verify the actual name from the discovery command above).
- `cs_binary_dest` — the path the unit executes (from `readlink` above).
- `cs_binary_src: /home/tony/lshulz/scion/bin/control` — the freshly built
  patched binary.

Then deploy:

```sh
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_scion_cs.yaml
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_linkd.yaml
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_dashboard.yaml
```

Run `deploy_scion_cs.yaml` before `deploy_linkd.yaml` so the one-time SIGHUP
`deploy_linkd.yaml` sends after installing the static advertisements lands on
a reload-capable CS. On a stock (unpatched) CS a SIGHUP just re-reads
`topology.json` — harmless — so this order is a nicety, not a requirement.

`deploy_linkd.yaml` installs scion-linkd on every AS container, copies the
per-AS `staticInfoConfig.base.json` and `linkd-baseline.json` to
`/etc/scion/AS<n>/`, installs `staticInfoConfig.json` as a copy of that base
file — the permanent story advertisements, unaffected by shaping — and
configures scion-linkd to listen on the container's management IP
(`10.20.3.15x:30480`) — AS containers are dual-homed on the public IETF net,
so it must not bind all interfaces. Static info stays static: linkd's own
staticinfo-writer + CS-SIGHUP machinery ships disabled by default
(`staticinfo_base = ""`, an escape hatch — see `linkd/internal/config`); the
playbook itself sends the CS a one-time SIGHUP, and only when the copied file
actually changed. `deploy_scion_cs.yaml` stops the CS, ships the patched
`bin/control`, restarts it, and verifies the SIGHUP reload path logs
`Reloaded static info`. `deploy_dashboard.yaml` installs fabricd and copies
only the AS topologies + core list it reads (never SCION private keys) to
`/etc/fabric/config/`.

The dashboard is reachable at `http://10.20.3.200:8080` (mgmt) and on its
public IETF net address (fabricd deliberately binds all interfaces). Append
`?mode=screen` to the URL for the big-screen display.

Everything fabricd serves — UI, shaping API, join, /play — requires HTTP
basic auth `scion:<booth_code>` once `booth_code` is set (one browser
prompt covers the dashboard and the playground terminals; `GET /api/health`
stays open). The venue leg additionally accepts :8080 only from
`venue_allowed_v4`/`venue_allowed_v6` (group_vars/playground.yml — IETF
meeting prefixes + the `10.0.0.0/24`/`10.1.0.0/24` lab subnets; confirm per
meeting).

### Bootstrap servers

Each AS container also runs a vendored `scion-bootstrap-server`, serving
that AS's `topology.json` + TRC over plain HTTP on the mgmt IP so endhosts
can bootstrap without hand-copying a bundle. Deploy **after** the base SCION
stack (`deploy_scion_stack.yaml`) has run — it needs `/etc/scion/AS<n>/` in
place:

```sh
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_bootstrap_server.yaml
```

Serves on `10.20.3.15x:8041` for every AS `15x`. Verify:

```sh
curl http://10.20.3.152:8041/topology
```

fabricd's `bootstrap_url_template = "http://10.20.3.%d:8041"` (set in
`deploy_dashboard.yaml`) turns this into the clickable bootstrap-URL links
on the `/join` page's per-AS tabs — see "Attendee access" below.

## Verify

```sh
python3 topology/verify_topology.py         # expect: OK: 24 links consistent
curl -s http://10.20.3.200:8080/api/health   # linkd map all 12 true, targets all true
                                              # (aggregate check; see Runbook below —
                                              # this replaces polling each AS's own
                                              # /healthz individually)

python3 topology/gen_staticinfo.py --check   # expect: OK: 12 ASes generated, files match
curl -s http://10.20.3.150:30480/api/v1/links | grep -o '"shaped":[a-z]*'   # all false at rest
curl -s http://10.20.3.150:30480/healthz     # status:ok; no metadata_ok/reload_ok fields
                                              # (writer disabled by default — see below)
# end-to-end: shape 155-158 in the dashboard —
#   scion showpaths --extended --refresh <dst>   # advertised latency is UNCHANGED, by design
#   dashboard TracePanel / idint-traceroute       # measured hop RTT reflects the shape
```

Static info stays static: advertisements (`staticInfoConfig.json`, installed
once by `deploy_linkd.yaml` as a copy of `staticInfoConfig.base.json` from
`topology/gen_staticinfo.py`) are the permanent story values. Shaping never
rewrites them and never SIGHUPs the CS as a side effect — it changes only
*measured* state (BFD RTT, ID-INT telemetry). The demo contrast is
deliberate: the TracePanel's "Σ … ms adv." stays put at the story value while
the measured hop RTT spikes, which is the reason ID-INT exists; the operator
pins an alternate path around the damage rather than waiting on an automatic
re-route (see "ID-INT path inspector" below). `query_interval = "30s"`
(sciond `[sd]` + control service `[path]`) is still deployed fleet-wide but
is now vestigial for the shaping story — segment refetch no longer carries a
shaping signal, since advertisements don't change; it still governs ordinary
beaconing/path-cache refresh.

## Runbook

### Health check

Prefer the aggregate endpoint over polling each AS individually:

```sh
curl -s http://10.20.3.200:8080/api/health   # /api/health is the only credential-free endpoint
```

Expect the `linkd` map to show all 12 ASes `true` and the `targets` map to
show all `true`. Anything `false` means fabricd can't reach that AS's linkd
(or its Prometheus target) — see "Wedged AS mid-demo" below.

### Demo morning

Restart fabricd to start from a clean process:

```sh
ansible -i ansible/inventory.yaml dashboard -b -m systemd -a 'name=fabricd state=restarted'
```

RTT baselines survive the restart via `baselines_path` (wired into
`deploy_dashboard.yaml` — see `dashboard/backend/cmd/fabricd/main.go`), so
links already shaped in a prior session don't need to be re-warmed. The one
case where this bites: if `topology/staticinfo.yml`'s story latencies are
ever regenerated and redeployed with different values, the persisted
baseline file (`/var/lib/fabricd/baselines.json` on the dashboard container)
must be deleted by hand before the restart — otherwise fabricd keeps judging
the new, legitimately different RTT against the stale old minimum and
misreports it as shaped/elevated.

Then warm the path cache so `showpaths` reflects current state immediately
rather than after the next refresh cycle:

```sh
scion showpaths --extended --refresh <dst>
```

### Wedged AS mid-demo

```sh
ansible -i ansible/inventory.yaml AS1-15x -b -a 'journalctl -u scion-linkd -n 50 --no-pager'
ansible -i ansible/inventory.yaml AS1-15x -b -m systemd -a 'name=scion-linkd state=restarted'
```

(`AS1-15x` — substitute the specific wedged AS, e.g. `AS1-155`.)

If it's the dashboard itself that looks wrong rather than one AS, check
fabricd's log instead:

```sh
ansible -i ansible/inventory.yaml dashboard -b -a 'journalctl -u fabricd -n 50 --no-pager'
```

### Idempotency and ordering

All three deploy playbooks (`deploy_scion_cs.yaml`, `deploy_linkd.yaml`,
`deploy_dashboard.yaml`) are idempotent — after a partial failure, just
re-run the whole playbook rather than hand-patching state.

Deploy `deploy_linkd.yaml` before `deploy_dashboard.yaml`: fabricd polls
linkd rather than the reverse, so getting the order backwards self-heals on
fabricd's next poll, but the dashboard looks empty for the few seconds until
that happens.

## Playground (Tier 1)

Hosted SCION endhosts attendees reach as a browser shell. Containers 210–213
— `play-158`, `play-152`, `play-155`, `play-161` — homed on the same AS set
attendees can WireGuard into (see "Attendee access" below), on the
mgmt+pubnet nets.

Build + deploy:

```sh
./tools/build-endhost.sh
./tools/build-scitra.sh      # -> .build/scitra/bin/{scitra-tun,scion2ip} (Docker, ubuntu:24.04 / glibc-2.39 target)
./tools/build-scion-apps.sh  # -> .build/scion-apps/bin/{scion-bwtestclient,scion-bwtestserver,scion-netcat,scion-bat}
./tools/build-curl.sh        # -> .build/curl/bin/curl (Docker; see "curl build" below)
cp ansible/group_vars/playground.yml.example ansible/group_vars/playground.yml  # set booth_code
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_playground.yaml
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_playground_apps.yaml
```

**curl build.** `./tools/build-curl.sh` builds a self-contained HTTP/3-capable
curl shipped to both the playground shells and the svc endhosts (see
"Service endhosts" below). It uses curl's OpenSSL-QUIC backend — a
from-source OpenSSL 3.5 (which has a native QUIC API) plus nghttp3 for HTTP/3
framing — rather than the ngtcp2 backend, which wants quictls/BoringSSL's
QUIC API that stock OpenSSL doesn't expose. Builds inside Docker
(`ubuntu:24.04`, matching the fleet's glibc 2.39); run it directly on the
host if Docker isn't available on your dev box. Output: `.build/curl/bin/curl`.

`deploy_playground_apps.yaml` layers the SCION app fleet onto the shells:
`scion-bwtestclient`/`scion-bwtestserver` (server on `:40002`), `scion-netcat`,
`scion-bat` (curl-like), the `scion2ip`/`ip2scion` address converters, `lynx`,
the HTTP/3 curl built above (shadows the distro curl on PATH), and a
`scapy-scion-int` venv (`/opt/scapy-scion-int/.venv`, wrapper `scapy-scion`)
whose interpreter is `setcap cap_net_raw,cap_net_admin` so it can send/sniff
raw SCION packets without sudo. Smoke tests (from a shell): `scion-bwtestclient -s
1-161,10.20.3.213:40002` (0% loss), `scion2ip 1-150 0 0 1` → `fc00:1000:9600::1`,
`scapy-scion -c "from scapy_scion.layers.scion import SCION"`.

Verify (from a laptop on pubnet):

1. Browse `http://<play-158 pubnet addr>:7681`, log in `scion` / booth code.
2. At the prompt: `scion showpaths 1-160 --extended` → paths listed.
3. `scion ping 1-161,10.20.3.213` → replies (proves play-161's shim answers).
   Don't use `…,127.0.0.1` as the target: the AS containers' dispatchers bind
   the mgmt IP, not loopback, so a loopback-addressed ping gets no reply.
4. Watch the dashboard map — traffic appears on 158↔ links.
5. Confinement check: `ssh 10.20.3.150` from the shell → hangs/blocked;
   `curl https://example.com` → blocked (nft drop). `nft list ruleset` on the
   container shows a non-zero drop counter after these.

Reset a wedged playground: `ansible-playbook -i ansible/inventory.yaml
ansible/playbooks/deploy_playground.yaml --limit play-152`, or recreate the
container from `create_contianers.sh`.

## ID-INT traceroute servers

Every AS container runs the ID-INT traceroute/debug tool
([netsys-lab/idint-traceroute](https://github.com/netsys-lab/idint-traceroute))
in server mode on **UDP 32001** (`idint-traceroute.service`, bound to the
mgmt IP). The playground hosts get the same binary for client use (no
service). The tool's go.mod pins the lschulz/scion fork at `8ce7ed2f857d` —
the deployed fork's upstream base — so it speaks the testbed's ID-INT wire
format. The servers are unauthenticated reflection targets reachable from
any AS over SCION — including attendee endhosts — which is the point; they
echo telemetry only.

```sh
./tools/build-idint-traceroute.sh   # -> .build/idint-traceroute/bin/idint-traceroute
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_idint_traceroute.yaml
```

Example client run (from any AS or playground container; the client's
`--local` port must differ from the server's 32001):

```sh
idint-traceroute --sciond 10.20.3.150:30255 --local 10.20.3.150:32000 \
  --remote 1-161,10.20.3.161:32001 -inst0 RTT_NEXT_BR -inst1 INGRESS_TSTAMP
```

`--sciond`/`--local` are per-host: on a playground container use
`--sciond 127.0.0.1:30255` and its own mgmt IP for `--local`.

## ID-INT path inspector

The dashboard's TRACE button drives live ID-INT probes across the fabric.
Each AS container runs `idint-probed.service` (HTTP **30490** on the mgmt IP,
stateless; probes go to the idint-traceroute reflectors on 32001 using the
AS's own sciond). fabricd holds ONE shared trace session — every dashboard
client sees the same trace, and it is lost on fabricd restart (just re-pin it
from the panel).

```sh
./tools/build-idint-probed.sh   # -> .build/idint-probed/bin/idint-probed
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_idint_probed.yaml
```

Deploy probers BEFORE enabling `[idint]` in the dashboard config (fabricd
degrades gracefully — the panel shows the prober error — but don't demo that).
Runbook: prober health is `curl http://10.20.3.15x:30490/api/v1/paths?dst=1-161`
from the host; a wedged prober is safe to `systemctl restart idint-probed`
(stateless).

`mock=true` fabricd demos the shaped-hop RTT spike fine, but NOT AUTO
re-route: mock paths are static and never re-rank by advertised latency, so
picking AUTO never jumps off a shaped path — that needs the live probers.
That said, AUTO follows advertised-fastest, which no longer tracks shaping
under the static-info model (see "Static info stays static" in Verify,
above) — so this holds with live probers too now; the re-route story is the
operator pinning an alternate path, not an automatic AUTO jump.

## Service endhosts (svc-150/151/152/153)

Headless SCION endhosts for hosting services behind scitra (e.g. DNS over
SCION) — one per core AS, each a privileged container with a venue leg
(`eth1` on `vmbr0`, DHCP v4 + SLAAC v6) so regular-IP clients on the venue
network can reach whatever's exposed, not only SCION-speaking peers. The
`svc` group in `ansible/inventory.yaml`:

| Host | CT | mgmt IP | AS |
|---|---|---|---|
| svc-151 | 214 | 10.20.3.214 | 151 |
| svc-150 | 215 | 10.20.3.215 | 150 |
| svc-152 | 216 | 10.20.3.216 | 152 |
| svc-153 | 217 | 10.20.3.217 | 153 |

Each runs the fork endhost stack (sciond with `experimental_idint = true`)
and `scitra-tun --scmp`; `idint-traceroute` is installed for client/debug use
(no server unit); fail2ban guards the sshd jail; ufw denies incoming by
default and trusts only the mgmt leg (`eth0`) — the venue leg is globally
routable, so exposing a service on it needs an explicit ufw allow alongside
the `scitra_extra_args` forward below.

All four are created by `proxmox/create_contianers.sh` along with the rest of
the fleet (Ubuntu 24.04 template, `--rootfs local-lvm:4`, `--features
nesting=1`, venue leg, and the `/dev/net/tun` passthrough the script applies
to 210–217) — no separate `pct create` step. Bootstrap fresh containers with
the usual `ietf`-user play; scope it to just the new hosts with `--limit` if
the rest of the fleet is already provisioned:

```sh
ansible-playbook -i ansible/inventory.yaml -e ansible_user=root \
  ansible/playbooks/bootstrap_ietf_user.yaml --limit svc-150,svc-152,svc-153
```

Deploy (reuses the endhost/scitra/idint-traceroute build outputs from the
Playground section above, plus `.build/curl/bin/curl` from `build-curl.sh` —
nothing new to build). `deploy_svc_endhost.yaml` covers the whole `svc`
group in one run:

```sh
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_svc_endhost.yaml
```

SSH access (mgmt is NATed, not routed from the LAN):
`ssh -J root@ietf-proxmox ietf@10.20.3.21x`, or `pct enter 21x` on the host
as the rescue hatch.

Hosting a service behind scitra: set `scitra_extra_args: " -p <port>"` on the
relevant host in `ansible/inventory.yaml` (static inbound forward, e.g.
`" -p 53"` for DNS) and rerun the playbook; the service is then reachable
over SCION at that host's fc00 address (printed in `journalctl -u scitra` on
the container).

Verify: `scion ping 1-151,10.20.3.214` (substitute the AS/mgmt IP for the
other three) from a playground shell replies (scitra answers SCMP echo);
pinging a svc host's fc00 address from a playground shell replies too.

## SCION DNS (CoreDNS on svc-152)

Resolves the `scion.` TLD (SVCB records carrying `scion=<IA>,<host>` next to
ordinary A/AAAA, per `docs/drafts/draft-john-scion-svcb-00.md`) and forwards
everything else to Quad9, on svc-152 (CT216, `10.20.3.216`). This is the DNS
half of the hev3 story below — hev3 resolves SVCB to learn a name's SCION
candidate alongside its IP ones.

### Build order

Two local forks feed the binary, both already on their `scion-dev` branch
and merged: `/home/tony/tjohn327/dns` (Task 1 — adds the `scion` SVCB
`SvcParamKey`, commit `f27c366e`) and `/home/tony/tjohn327/coredns` (Task 2
— `replace`s in the dns fork and vendors the
[netsys-lab/coredns-scitra](https://github.com/netsys-lab/coredns-scitra)
plugin so `scitra`'s AAAA-synthesis runs inside CoreDNS itself, commit
`57c13a5f`). Nothing to change here — just build:

```sh
./tools/build-coredns.sh   # -> .build/coredns/bin/coredns (COREDNS_SRC override; default /home/tony/tjohn327/coredns)
```

Fully static (`CGO_ENABLED=0`), so — unlike the idint-* binaries — there's no
GLIBC ceiling guard to check before shipping it to the Ubuntu 24.04 fleet.

### Deploy

```sh
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_coredns.yaml
```

Play 1 installs CoreDNS on svc-152 alone: copies the binary plus
`config/coredns/{Corefile,scion.zone}`, renders the unit, restarts, and
verifies both `dig SVCB web.scion` (non-empty — the `scion.` zone answers)
and `dig A one.one.one.one` (non-empty — the Quad9 forward path works).

Play 2 rolls `systemd-resolved` over to CoreDNS on every playground + svc
host (`playground:svc`), dropping in
`/etc/systemd/resolved.conf.d/scion-dns.conf` with `DNS=10.20.3.216` +
`Domains=~.` so **all** queries — not just `scion.` — go through CoreDNS,
then verifies `resolvectl query web.scion` resolves locally. This play is
gated behind `coredns_resolver_rollout` (default `true`); skip it with
`-e coredns_resolver_rollout=false` if you want CoreDNS running on svc-152
without repointing every other host's resolver yet (e.g. bringing the
service up ahead of a demo without touching a fleet that's mid-session).

### Zone / TXT rule

`config/coredns/scion.zone` draws a hard line the zone editor must never
cross: **dual-homed names — `web` and `web2`, the ones with a real fabric
A/AAAA answer alongside their SCION SVCB — must never also carry a `scion=`
TXT record.** The vendored `scitra` plugin synthesizes an AAAA answer from a
`scion=` TXT independently of
whatever the `file` plugin has for that name; on a name that already has a
real fabric AAAA, that synthesis *hijacks* the answer instead of
supplementing it, silently breaking the IP leg of the hev3 race. TXT is
reserved for SCION-only names (`games`, `matrix.netsys.ovgu` in the shipped
zone) that have no fabric address to protect.

### Zone sync

Zone sync: RETIRED (2026-07-11, BGP-fabric batch). web/web2 now carry static
fabric addresses (10.150.0.80 / 10.153.0.80 + fd00:beef ULAs) — no
venue-dependent records remain, so the sync timer's only remaining power was
to roll the zone back to stale venue IPs. If venue-dependent names ever
return, revive proxmox/coredns-zone-sync.* from git history. On the host:
systemctl disable --now coredns-zone-sync.timer && rm -f
/etc/systemd/system/coredns-zone-sync.{service,timer}.

## BGP fabric

A real IP underlay laid over the flat mgmt L2, so endhosts have genuine IPv4
and IPv6 legs to race against SCION (the hev3 story below) instead of the
single flat `10.20.3.0/24`. Each of the 12 AS containers runs BIRD 2 speaking
BGP to its inter-AS neighbours over the SCION link bridges, advertising its own
`10.<AS>.0.0/16` + `fd00:beef:<AS>::/48` and learning the rest — so a packet
from an endhost in AS158 to `10.150.0.80` traverses the fabric hop by hop,
following the same topology the SCION plane does. Generator + per-AS configs
are `topology/gen_bird.py` → `config/AS*/bird.conf` (spec
`docs/superpowers/specs/2026-07-11-bgp-fabric-design.md`).

### Deploy

```sh
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_bird.yaml
```

Play 1 (`ases`) installs BIRD 2 on the 12 AS containers, drops the forwarding
+ no-`send_redirects` sysctls (redirects would install flat-L2 shortcuts that
bypass the fabric — AS152 carries two endhosts on the L2), creates the
`fabric0` dummy netdev holding the AS anchor addresses (`10.<AS>.0.1/32` +
`fd00:beef:<AS>::1/128`), lays an `eth0` drop-in for the v6 gateway
(`::fffe/64`) and the on-link `10.<AS>.0.0/24` delivery route, installs
`bird.conf`, and starts BIRD — a config-only change is applied with `birdc
configure` (graceful; sessions and BFD state survive), never a restart. It
finishes by asserting the live session count matches the config. Play 2
(`playground:svc`) attaches the endhosts: host-scoped (`/32`, `/128`) fabric
addresses plus routes for every `10.15x.0.0/16` and `fd00:beef::/32` via the
home AS's mgmt IP; svc hosts additionally gain the `.81` SCION-underlay sibling
and a table-100 return-path so fabric-sourced replies to WG attendees ride the
fabric while mgmt-sourced SCION underlay keeps its hub path. Play 3 (`hub`,
CT201) gives the hub a stable /128 in each wg_as segment (`fd00:beef:{152,155,158,161}::c8`) and routes
into the fabric via AS155, and audits the hub's NAT for a masquerade that could
rewrite fabric-destined WG traffic (a manual check, printed — a masqueraded
source pulls replies onto the flat L2 and halves measured RTT).

`deploy_bird.yaml` is idempotent — after a partial failure just re-run it. The
networkd drop-ins land under `/etc/systemd/network/*.network.d/` and reload via
`networkctl reload`; the `pct`-managed `eth0.network` they extend must already
exist (each play asserts it).

Run this before `deploy_hev3.yaml`: Play 2's `.81` sibling address on the svc
hosts' `eth0` is what `hev3-server -scion-ip` binds to, and deploying hev3
first crash-loops it with `EADDRNOTAVAIL` (see the hev3 section below).

### Demo

```sh
traceroute 10.155.0.1                          # from an endhost: hop-by-hop across the fabric to an AS anchor
birdc -r show protocols                        # on an AS container: BGP sessions + BFD state, all up
curl -s http://10.20.3.155:30480/api/v1/bgp    # linkd's view of that AS's sessions (feeds the dashboard badge)
```

Every AS anchor is `10.<AS>.0.1` (150–161); a traceroute to one from an endhost
shows the fabric path, distinct from the SCION plane's. The dashboard derives
its per-AS BGP badge from the linkd `/api/v1/bgp` endpoint above.

BFD makes the fabric reactive to link shaping: **≥30% loss flaps BGP within
minutes — a feature, narrate it.** Shaping a link to heavy loss in the
dashboard tears the BGP session down, the fabric reconverges around it, and the
badge goes red — the IP underlay visibly reacts to the same `tc` the SCION
story rides on.

### WireGuard dual-stack confs

The fabric gives WG attendees a routable v6 (and v4) leg into the testbed, so
the issued confs are now dual-stack. Regenerate the pool and redeploy the hub
and dashboard in this order:

```sh
./tools/gen-wg-pool.sh -f                      # dual-stack pool; -f is destructive — see "Pool exhausted"
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_wghub.yaml
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_dashboard.yaml
```

`deploy_wghub.yaml` before `deploy_dashboard.yaml` for the same reason as the
Attendee-access section: fabricd reads the pool file at startup and
`log.Fatalf`s if it's missing, so a dashboard deploy ahead of the hub takes the
whole dashboard down. `gen-wg-pool.sh -f` invalidates every conf already handed
out — only regenerate between sessions (see "Pool exhausted" below).

## Happy Eyeballs v3 demo (hev3)

`hev3` is the SCION-aware Happy Eyeballs v3 CLI
(`draft-ietf-happy-happyeyeballs-v3` extended with a SCION candidate family —
see `docs/superpowers/specs/2026-07-10-scion-svcb-hev3-design.md`): it
resolves a name's SVCB record, then races SCION, IPv6, and IPv4 candidates
in parallel and reports the winner. `hev3-server` is the demo target it
races toward — one process serving the same page over IP h2/h1.1, IP HTTP/3,
and (with `-scion`) native HTTP/3 over SCION QUIC, tagging each response
with the transport that actually won.

### Build

```sh
cd hev3 && make deb   # -> dist/scion-hev3_0.1.0_amd64.deb
```

Ships `/usr/local/bin/{hev3,hev3-server}` plus the testbed CA at
`/etc/hev3/ca.pem` (a conffile — the `hev3` CLI trusts it by default). The
CA and the per-name `web.scion`/`web2.scion` leaf certs it signs live at
`ansible/files/hev3-ca/` and are deliberately committed (throwaway
testbed-only key material — see `ansible/files/hev3-ca/README.md`);
regenerate/rotate with:

```sh
tools/gen-hev3-ca.sh
```

### Deploy

`deploy_bird.yaml` (BGP fabric endhost attachment, above) must run before this
playbook on the svc hosts: `hev3-server`'s `-scion-ip 10.<AS>.0.81` bind fails
(`EADDRNOTAVAIL`, `Restart=on-failure` crash-loop) until Play 2 of the fabric
deploy has put the `.81` sibling address on `eth0`. If `hev3-server` was
deployed first, rerun `deploy_bird.yaml` then restart the `hev3-server` unit
on the affected svc hosts.

```sh
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_hev3.yaml
```

Play 1 installs the `scion-hev3` deb (CLI + CA) on every playground and svc
host. Play 2, scoped to `svc-150`/`svc-153` only, copies each host's
per-name cert/key pair from `ansible/files/hev3-ca/` to
`/etc/hev3/{cert,key}.pem`, renders `hev3-server.service` (runs
`hev3-server -scion -scion-ip 10.<AS>.0.81` — the SCION underlay binds the
fabric sibling `.81` so its UDP :443 can't collide with the ip-h3 :443 on the
fabric primary `.80`; `fabric_scion_ip` is set from `endhost_as`),
`SCION_DAEMON_ADDRESS=127.0.0.1:30255`, `After`/
`Wants`/`Requires` the same `scion-sciond` unit the svc endhost stack
installs), opens `443/tcp` + `443/udp` in ufw (the venue leg denies incoming
by default — see "Service endhosts" above — so the IP race target needs an
explicit allow; SCION traffic rides the existing underlay and needs no ufw
rule), and verifies `curl -k https://127.0.0.1/whoami` plus both listeners
in `ss`.

### Demo runbook

From a playground shell (`play-158`, once CoreDNS's resolver rollout has
landed there — see above):

```sh
hev3 https://web.scion/
```

prints a race table (SCION / IPv6 / IPv4 candidates, start time, outcome,
winner) followed by the response body — `web.scion` resolves to svc-150
(`1-150,10.150.0.81`) over SCION and to svc-150's fabric address
(`10.150.0.80`) over IP. Confirm the SVCB record directly:

```sh
dig SVCB web.scion @10.20.3.216
```

To see the SCION leg actually get slower (not just faster than nothing):
shape a link on the 158->150 path in the dashboard, then rerun `hev3
https://web.scion/` and watch the winner and per-candidate timings move.
Attendee WireGuard clients need no extra setup for any of this — every
issued conf already carries `DNS = 10.20.3.216`, so `scion.` names resolve
automatically over the tunnel.

**Parked caveat, stated plainly:** on the real venue/mgmt network, the IP
legs (venue Wi-Fi/wired, or the mgmt LAN) will typically win the race
against the *emulated* SCION latencies this testbed applies via `tc` —
real fabric SCION isn't slower than IP here, the shaped link is. This is
known, acknowledged, and **deliberately not mitigated yet**: the plan is to
implement the race correctly first and tackle that trap later, not paper
over it with a thumb on the scale.

## Attendee access (Tier 2 — WireGuard)

Attendees' own laptops join as real SCION endhosts in ASes 1-152, 1-155,
1-158, 1-161 over WireGuard, via the `/join` page on the dashboard. This is
separate from the Tier 1 browser-terminal playground above — the WG hub
(CT201) and the dashboard's join API (fabricd, CT200) are the two containers
involved.

**Join flow.** Claiming is booth-code-only — there's no AS picker before you
claim, and one conf tunnels the whole testbed. After claiming, the join page
shows a tab per `joinable_ases` entry (152, 155, 158, 161): each tab carries
its own downloadable endhost bundle and scitra fc00 identity, plus a
clickable bootstrap-server URL (`http://10.20.3.15x:8041`, from
`bootstrap_url_template` in `deploy_dashboard.yaml` — see "Bootstrap
servers" above) as an alternative to hand-unpacking the bundle, for sciond
builds that support HTTP bootstrap.

### Build + deploy order

```sh
./tools/gen-wg-pool.sh                        # -> .build/wghub/{wg0.conf,pool.json}, 50 slots
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_wghub.yaml
cd dashboard/web && npm run build             # skip if already built this session
cd ../backend && make deb
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_dashboard.yaml
```

`gen-wg-pool.sh` refuses to overwrite an existing pool unless you pass `-f` —
**regenerating with `-f` replaces the server key and all 50 peer keys, which
invalidates every conf already handed out**, so only do this deliberately
(see "Pool exhausted" below). `deploy_wghub.yaml` installs the hub (wg0 +
nftables on CT201), pushes `pool.json` to CT200 as
`/var/lib/fabricd/wg-pool.json`, and installs the return-route unit on the
`wg_ases` group (AS152/155/158/161) (`ip route 10.20.5.0/24 via <hub>` —
without this the reply half of an attendee's tunnel never gets back to the
hub). This must run **before** `deploy_dashboard.yaml`: with
`join_enabled = true`, fabricd reads the pool file at startup and exits
(`log.Fatalf`) if it's missing, so deploying the dashboard first with no
pool in place takes the whole dashboard down, not just the join feature.

### On-site bring-up

The hub's venue-facing address isn't known until the container is racked and
plugged into the venue network — and because the hub leg is DHCP/SLAAC, its
address also changes on every rebuild or lease renewal. So the advertised
`wg_endpoint_v6`/`wg_endpoint_v4` (in `ansible/group_vars/playground.yml`,
gitignored) go stale and attendees' `wg-quick up` hits the old address. Rather
than editing them by hand, derive them from the hub's **current** address and
redeploy in one step (run on the Proxmox host, after the hub is on the venue
net):

```sh
bash tools/update-wg-endpoint.sh
```

It reads CT201's `eth1`, picks the v4 plus the best global v6 (a real
global-unicast `2000::/3` venue address over a `fc00::/7` ULA, never a
temporary/privacy address — the IETF net is v6-mostly, so the v6 endpoint is
the one attendees use), writes both into `group_vars`, and reruns
`deploy_dashboard.yaml`. Verify with
`curl -su scion:<code> http://<dash>:8080/api/join/meta` (`endpoint_v6` should
match the hub). Re-run it any time the hub's address changes.

(Only `deploy_dashboard.yaml` needs rerunning — the endpoints are injected into
each conf at claim time, not baked into the hub's own `wg0.conf`. The join page
hands out the **v6** conf by default; v4 stays as a fallback for v4-only
clients.)

### Wi-Fi-uplink contingency

If the booth table has no wired drop, the host itself joins the venue SSID
and NATs the two containers out through it instead of a routed uplink. Port-
forward `51820/udp -> CT201` and `8080/tcp -> CT200` on the host, and set the
join endpoints to the **host's** address (not CT201's) in
`group_vars/playground.yml`, since that's what's actually reachable from the
venue side of the NAT. WireGuard's handshake is a single client-initiated
UDP flow in each direction, so it survives ordinary NAT without extra
keepalive tuning beyond the `PersistentKeepalive = 25` already baked into
every issued conf. Treat this as a fallback, not the primary plan — prefer a
wired drop with routed addressing when one's available.

If the host's `8080/tcp -> CT200` port-forward masquerades/SNATs the
forwarded traffic (rather than a plain DNAT that preserves client source
addresses), CT200 sees the connection arriving from the host's own internal
address, not the attendee's. In that case make sure `venue_allowed_v4` in
`ansible/group_vars/playground.yml` also includes that NAT source subnet —
otherwise CT200's venue allowlist drops the forwarded traffic and the
dashboard is unreachable through the fallback even though the port-forward
itself is working.

### Runbook

**Revoke a conf** (lost laptop, leaked key, attendee leaving):

```sh
ssh wg-hub sudo wg set wg0 peer <PUBKEY> remove
```

Then on CT200, edit `/var/lib/fabricd/wg-claims.json`, add the slot number to
the `"burned"` array, and restart fabricd:

```sh
systemctl restart fabricd
```

Burned slots are never reissued — this permanently gives up one of the 50
slots, not just the current claim.

**Rotate the booth code**: edit `booth_code` in `group_vars/playground.yml`,
then rerun both `deploy_dashboard.yaml` and `deploy_playground.yaml` — the
same code gates the WG join claim and the Tier 1 ttyd terminal login, so
both need redeploying to stay in sync.

**Update the venue prefixes** (per meeting): edit `venue_allowed_v4` /
`venue_allowed_v6` in `ansible/group_vars/playground.yml`, then rerun
`ansible-playbook -i ansible/inventory.yaml
ansible/playbooks/deploy_dashboard.yaml`.

**Pool exhausted**: all 50 slots claimed shows as an expected 409 ("no confs
left — ask at the booth") in the join UI, not a bug. The only way to free
capacity is to regenerate the pool — `./tools/gen-wg-pool.sh -f`, then
redeploy the hub and dashboard — which **invalidates every conf already
issued**. Only do this between sessions, and announce it at the booth first
so attendees mid-tunnel know to re-claim.

**Restart matrix** — what survives what:

| Event | Effect |
|---|---|
| Hub (CT201) reboot | Tunnels resume on their own — `wg0` is a static `wg-quick` conf brought up by systemd, no daemon holding state. |
| fabricd restart | Claims persist — `wg-claims.json` is written atomically on every claim/burn. |
| `deploy_playground.yaml` rerun | Tier 1 ttyd terminals blip; Tier 2 tunnels are unaffected (different containers, no shared state). |

Two standing facts worth keeping in mind while debugging either tier: the
management plane (`10.20.3.0/24`) is unreachable from the venue network
except through the WG tunnel, and WG egress is pinned by the hub's nftables
`forward` chain to AS152/155/158/161 only — an attendee's tunnel can reach their own
AS's border router/control service and hairpin to other attendees, nothing
else on the mgmt net.

### NOC email

Send before the event, once the hub's venue uplink is provisioned:

```
Subject: IETF 126 SCION hackathon testbed — port check

Two services on our hackathon table are reachable from the general
attendee network:

  - Live topology dashboard: TCP/8080 to <dashboard-venue-addr>
  - WireGuard attendee access: UDP/51820 to <hub-venue-addr>

Both need to stay open inbound from the venue Wi-Fi/wired segments to our
table. Nothing else on our subnet should be attendee-reachable.

Thanks,
<contact>
```

Client-to-client traffic on the venue network is **not** filtered (confirmed
2026-07-06), so WireGuard attendees on Wi-Fi can reach the hub on our wired
drop — the email above is a courtesy heads-up, not a go/no-go dependency. (The
direct-underlay approach would also work given this, but we keep the tunnel
for the scitra IPv6 story and mgmt-plane isolation.)

### On-site checklist

```
[ ] Wired drop live: CT200/CT201 have DHCP leases on eth1 (venue net)
[ ] Hub has a global IPv6 on eth1 (pct exec 201 -- ip -6 -br addr show eth1 scope global)
[ ] Pre-flight ON the proxmox host: sudo tools/wg-attendee-test.sh <hub-v6-or-v4>
    passes — this is a netns simulation run on the host (it needs root, makes a
    netns, reads a slot key from .build/wghub/pool.json); it proves the hub
    answers and the tunnel path works end to end. NOT an attendee-laptop test.
[ ] Real SSID-client test from an actual laptop on the ietf SSID: open the
    dashboard join page, claim a conf, wg-quick up it, then scion showpaths /
    scion ping per the laptop instructions — the genuine "from a laptop" path
    uses the join page + real SCION tools, not the host script above.
[ ] One claim per SSID: run the claim flow once per venue SSID, not just one
[ ] QR code on the join page scans and imports on a phone WireGuard app
[ ] /play/158/ (Tier 1 terminal) loads and logs in from a phone browser
[ ] nft drop counters on the hub bump after a :22 probe to 10.20.3.150 (a
    non-joinable AS / the mgmt plane) from inside the tunnel — mirrors the
    Tier-1 confinement check. The hub's forward chain ACCEPTS tcp to
    10.20.3.152/155/158/161, so the probe must target an AS outside that set
    to drop; the wg0->eth0 pin to 152/155/158/161 only is what bumps the
    drop counter.
```

### Known gotchas

- **`deploy_linkd.yaml` still uses `ansible.builtin.apt`, not `dpkg -i`.**
  `deploy_dashboard.yaml` was fixed to install fabricd via `dpkg -i` because
  `apt`/`apt-get` no-op when the target deb is byte-identical in version to
  what's already installed — a real risk when iterating and rebuilding
  without bumping the version string. `deploy_linkd.yaml` has not been fixed
  the same way: a rebuilt scion-linkd deb with an unchanged version number
  will report success and **not actually install**. Either bump
  `scion-linkd`'s version before rebuilding, or `dpkg -i` the deb on the AS
  containers by hand.
- **MTU.** Attendee tunnels report a 1380 MTU, but the real ceiling is lower
  once SCION + WireGuard headers are accounted for — tell attendees to keep
  tunnelled payloads under ~1200 bytes (see `dashboard/instructions/faq.md`
  for the full explanation, which is also what the join page links to).
- **`gen-wg-pool.sh -f` is destructive with no confirmation prompt beyond the
  flag itself** — see "Pool exhausted" above. There's no partial-regenerate;
  it's all 50 slots or none.

## Dev loop

Run the backend against mock data and the frontend dev server side by side,
no containers needed:

```sh
cd dashboard/backend && go run ./cmd/fabricd -config <cfg with mock=true>
cd dashboard/web && npm run dev   # proxies /api to 127.0.0.1:8080
```
