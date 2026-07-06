# Deploy runbook

Build and deploy the SCION Fabrik dashboard (fabricd + web) and the
link-shaping daemon (scion-linkd) to the IETF 126 testbed.

## Host network

The Proxmox host's testbed bridges are defined canonically in
`proxmox/interfaces.d-scion-testbed`, installed to
`/etc/network/interfaces.d/scion-testbed` and applied with `ifreload -a`. It
defines the isolated `mgmt` bridge (host `10.20.3.1/24`, `bridge-ports none`)
that carries the internal management plane `10.20.3.0/24` and NATs it out via
the venue uplink (`vmbr0`), plus the 24 `scion*` inter-AS link bridges.
Containers attach `eth0` to `mgmt` with static `10.20.3.<id>` addresses (see
`proxmox/create_contianers.sh`); only the dashboard (CT200) and wg-hub
(CT201) also carry a venue leg (`eth1` on `vmbr0`).

Playground containers (CT210–213) additionally need `/dev/net/tun`
passthrough for `scitra-tun`: two raw lines in each container's
`/etc/pve/lxc/<id>.conf` — `lxc.cgroup2.devices.allow: c 10:200 rwm` and
`lxc.mount.entry: /dev/net/tun dev/net/tun none bind,create=file` — plus
`tun` in the host's `/etc/modules-load.d/` so the module is loaded at boot.
Requires a container restart to take effect; already applied to CT210–213.

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

Run `deploy_scion_cs.yaml` before `deploy_linkd.yaml` so the first SIGHUP
linkd sends lands on a reload-capable CS. On a stock (unpatched) CS a SIGHUP
just re-reads `topology.json` — harmless — so this order is a nicety, not a
requirement.

`deploy_linkd.yaml` installs scion-linkd on every AS container, copies the
per-AS `staticInfoConfig.base.json` and `linkd-baseline.json` to
`/etc/scion/AS<n>/`, and configures it to listen on the container's
management IP (`10.20.3.15x:30480`) — AS containers are dual-homed on the
public IETF net, so it must not bind all interfaces. `deploy_scion_cs.yaml`
stops the CS, ships the patched `bin/control`, restarts it, and verifies the
SIGHUP reload path logs `Reloaded static info`. `deploy_dashboard.yaml`
installs fabricd and copies only the AS topologies + core list it reads
(never SCION private keys) to `/etc/fabric/config/`.

The dashboard is reachable at `http://10.20.3.200:8080` (mgmt) and on its
public IETF net address (fabricd deliberately binds all interfaces). Append
`?mode=screen` to the URL for the big-screen display.

## Verify

```sh
python3 topology/verify_topology.py         # expect: OK: 24 links consistent
curl -s http://10.20.3.200:8080/api/health   # linkd map all 12 true, targets all true
                                              # (aggregate check; see Runbook below —
                                              # this replaces polling each AS's own
                                              # /healthz individually)

python3 topology/gen_staticinfo.py --check   # expect: OK: 12 ASes generated, files match
curl -s http://10.20.3.150:30480/api/v1/links | grep -o '"shaped":[a-z]*'   # all false at rest
curl -s http://10.20.3.150:30480/healthz     # metadata_ok:true reload_ok:true
# end-to-end: shape 155-158 in the dashboard, then within ~30 s:
#   scion showpaths --extended --refresh <dst>   # latency reflects the change
```

Beacon metadata propagates at beaconing speed (origination/propagation/
registration + the sciond path cache), not instantly — expect the shaped
change to show up in `showpaths` roughly 10-30 s after shaping, not
immediately.

## Runbook

### Health check

Prefer the aggregate endpoint over polling each AS individually:

```sh
curl -s http://10.20.3.200:8080/api/health
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
(`play-158`…`play-161`) on the mgmt+pubnet nets.

Build + deploy:

```sh
./tools/build-endhost.sh
./tools/build-scitra.sh    # -> .build/scitra/bin/scitra-tun (Docker build, Debian-12 glibc target)
cp ansible/group_vars/playground.yml.example ansible/group_vars/playground.yml  # set booth_code
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_playground.yaml
```

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
ansible/playbooks/deploy_playground.yaml --limit play-159`, or recreate the
container from `create_contianers.sh`.

## ID-INT traceroute servers

Every AS container runs the ID-INT traceroute/debug tool
([netsys-lab/idint-traceroute](https://github.com/netsys-lab/idint-traceroute))
in server mode on **UDP 32001** (`idint-traceroute.service`, bound to the
mgmt IP). The playground hosts get the same binary for client use (no
service). The tool's go.mod pins the lschulz/scion fork at `8ce7ed2f857d` —
the deployed fork's upstream base — so it speaks the testbed's ID-INT wire
format.

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

## Attendee access (Tier 2 — WireGuard)

Attendees' own laptops join as real SCION endhosts in ASes 1-158..1-161 over
WireGuard, via the `/join` page on the dashboard. This is separate from the
Tier 1 browser-terminal playground above — the WG hub (CT201) and the
dashboard's join API (fabricd, CT200) are the two containers involved.

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
`/var/lib/fabricd/wg-pool.json`, and installs the return-route unit on
AS158-161 (`ip route 10.20.5.0/24 via <hub>` — without this the reply half of
an attendee's tunnel never gets back to the hub). This must run **before**
`deploy_dashboard.yaml`: with `join_enabled = true`, fabricd reads the pool
file at startup and exits (`log.Fatalf`) if it's missing, so deploying the
dashboard first with no pool in place takes the whole dashboard down, not
just the join feature.

### On-site bring-up

The hub's venue-facing address isn't known until the container is racked and
plugged into the venue network, so `wg_endpoint_v6`/`wg_endpoint_v4` in
`ansible/group_vars/playground.yml` (gitignored, copy from
`playground.yml.example`) start as placeholders and must be filled in on
site:

```sh
pct exec 201 -- ip -6 -br addr show eth1 scope global   # hub venue v6
pct exec 201 -- ip -4 -br addr show eth1                # hub venue v4
# set wg_endpoint_v6 / wg_endpoint_v4 in ansible/group_vars/playground.yml
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_dashboard.yaml
```

(Only `deploy_dashboard.yaml` needs rerunning — the endpoints are baked into
fabricd's rendered conf, not into the hub's own `wg0.conf`.)

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
`forward` chain to AS158-161 only — an attendee's tunnel can reach their own
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
    10.20.3.158-161, so the probe must target an AS outside that set to drop;
    the wg0->eth0 pin to 158-161 only is what bumps the drop counter.
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
