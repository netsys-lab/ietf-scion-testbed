# Deploy runbook

Build and deploy the SCION Fabrik dashboard (fabricd + web) and the
link-shaping daemon (scion-linkd) to the IETF 126 testbed.

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

- Fork: `$SCION_FORK`, default `/home/tony/lshulz/scion`.
- Branch: `staticinfo-sighup`.
- Commit: `158d2060b`, based on upstream `8ce7ed2f8` (the commit pinned for
  this deploy).

**This branch is currently local-only** — it has not been pushed to any
remote. The CS binary already running on the testbed was built from it on
the original build host, but anyone else (a fresh checkout, a different
build host, a teammate picking this up) cannot build a reload-capable CS
until the branch is pushed somewhere shared. Pushing it is the deploying
operator's call to make by hand; nothing in this repo's automation does it
for you.

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
cp ansible/group_vars/playground.yml.example ansible/group_vars/playground.yml  # set booth_code
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_playground.yaml
```

Verify (from a laptop on pubnet):

1. Browse `http://<play-158 pubnet addr>:7681`, log in `scion` / booth code.
2. At the prompt: `scion showpaths 1-160 --extended` → paths listed.
3. `scion ping 1-161,127.0.0.1` → replies (proves the shim answers).
4. Watch the dashboard map — traffic appears on 158↔ links.
5. Confinement check: `ssh 10.20.3.150` from the shell → hangs/blocked;
   `curl https://example.com` → blocked (nft drop). `nft list ruleset` on the
   container shows a non-zero drop counter after these.

Reset a wedged playground: `ansible-playbook ... deploy_playground.yaml
--limit play-159`, or recreate the container from `create_contianers.sh`.

## Dev loop

Run the backend against mock data and the frontend dev server side by side,
no containers needed:

```sh
cd dashboard/backend && go run ./cmd/fabricd -config <cfg with mock=true>
cd dashboard/web && npm run dev   # proxies /api to 127.0.0.1:8080
```
