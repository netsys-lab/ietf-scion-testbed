# SCION Fabrik — dashboard frontend

React + TypeScript frontend for the SCION Fabrik dashboard: a live transit-map
visualization of the IETF 126 SCION testbed (12 ASes, 24 inter-AS links),
showing topology, traffic, and per-link shaping controls. Talks to the
`fabricd` backend (`dashboard/backend`) over REST + WebSocket.

## Dev commands

- `npm run dev` — start the Vite dev server (proxies `/api` to
  `127.0.0.1:8080`). Run `fabricd` in mock mode alongside it:
  `go run ./cmd/fabricd -config <cfg with mock=true>` from
  `dashboard/backend`.
- `npm test` — run the unit tests (vitest).
- `npm run build` — type-check and produce a production build in `dist/`.
  This is what `dashboard/backend`'s `make deb` bundles into the fabricd
  package — build the frontend before building the backend deb.
- `npm run lint` — oxlint.

Append `?mode=screen` to the dashboard URL for the big-screen display mode.

See `/DEPLOY.md` at the repo root for the full build and deploy runbook.
