# Deploy runbook

Build and deploy the SCION Fabrik dashboard (fabricd + web) and the
link-shaping daemon (scion-linkd) to the IETF 126 testbed.

## Prereqs

- Go 1.22+, Node 22+, `dpkg-deb` (build host).
- `ansible` on the management host, with SSH access to the AS containers and
  the dashboard container per `ansible/inventory.yaml`.

## Build order

Build the frontend before the backend deb — `fabricd`'s `make deb` bundles
`dashboard/web/dist` into the package and **fails if it's missing** (set
`SKIP_WEB=1` to intentionally build a headless deb, e.g. for backend-only
iteration).

```sh
cd dashboard/web && npm ci && npm run build
cd ../backend && make deb
cd ../../linkd && make deb
```

Both `make deb` targets produce `dist/*_0.1.0_amd64.deb`.

## Deploy

```sh
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_linkd.yaml
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_dashboard.yaml
```

`deploy_linkd.yaml` installs scion-linkd on every AS container and configures
it to listen on the container's management IP (`10.20.3.15x:30480`) — AS
containers are dual-homed on the public IETF net, so it must not bind all
interfaces. `deploy_dashboard.yaml` installs fabricd and copies only the AS
topologies + core list it reads (never SCION private keys) to
`/etc/fabric/config/`.

The dashboard is reachable at `http://10.20.3.200:8080` (mgmt) and on its
public IETF net address (fabricd deliberately binds all interfaces). Append
`?mode=screen` to the URL for the big-screen display.

## Verify

```sh
python3 topology/verify_topology.py         # expect: OK: 24 links consistent
curl http://10.20.3.15x:30480/healthz        # per AS, x = 0..1
curl http://10.20.3.200:8080/api/health
```

## Dev loop

Run the backend against mock data and the frontend dev server side by side,
no containers needed:

```sh
cd dashboard/backend && go run ./cmd/fabricd -config <cfg with mock=true>
cd dashboard/web && npm run dev   # proxies /api to 127.0.0.1:8080
```
