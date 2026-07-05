# Deploy runbook

Build and deploy the SCION Fabrik dashboard (fabricd + web) and the
link-shaping daemon (scion-linkd) to the IETF 126 testbed.

## Prereqs

- Go 1.22+, Node 22+, `dpkg-deb` (build host).
- `ansible` on the management host, with SSH access to the AS containers and
  the dashboard container per `ansible/inventory.yaml`.

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
(cd /home/tony/lshulz/scion && go build -o bin/control ./control/cmd/control)
```

Both `make deb` targets produce `dist/*_amd64.deb` (`linkd` ships
`scion-linkd_0.2.0_amd64.deb`, with the beacon-metadata config keys and CS
reload support). The last command builds the patched control-service binary
(staticinfo SIGHUP reload) from the fork.

## Deploy

One-time, before the first deploy (or whenever the CS unit/binary naming on
the containers is in doubt): discover the actual systemd unit name and binary
path for the control service, since these vary by how the containers were
provisioned:

```sh
ansible -i ansible/inventory.yaml ases -b \
  -m shell -a 'systemctl list-units "*scion*" --no-legend; readlink /proc/$(pgrep -o -f cs1-150)/exe'
```

Record the results in `ansible/inventory.yaml` (or a `group_vars/ases.yml`)
as three vars on the `ases` group:

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
curl http://10.20.3.15x:30480/healthz        # per AS, x = 0..1
curl http://10.20.3.200:8080/api/health

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

## Dev loop

Run the backend against mock data and the frontend dev server side by side,
no containers needed:

```sh
cd dashboard/backend && go run ./cmd/fabricd -config <cfg with mock=true>
cd dashboard/web && npm run dev   # proxies /api to 127.0.0.1:8080
```
