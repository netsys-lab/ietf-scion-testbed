# Booth demo cheat sheet

Every command verified live 2026-07-11. Run from a playground shell
(`ietf@10.20.3.210` = play-158, or a `/play/<AS>` browser terminal) unless
noted. Dashboard: `http://10.20.3.200:8080` (booth code auth, user `scion`).
**Before opening the booth: `bash tools/booth-check.sh` — all lines OK.**

## 1. The race (fair, same links)

```sh
hev3 https://web.scion/            # SCION wins ~150ms native h3
hev3 --no-scion https://web.scion/ # IP legs only: v6 wins ~50ms over the BGP fabric
```
All rows show fabric addresses (`10.150.0.80` / `fd00:beef:150::80` /
`1-150,10.150.0.81`). Both planes cross the SAME emulated links — that's
the point. `web2.scion` = svc-153, same demo, different paths.

## 2. Degradation: SCION reroutes, BGP endures

Shape link 150↔154 (dashboard slider, or):
```sh
curl -su scion:<booth> -X PUT http://10.20.3.200:8080/api/links/150-154/shaping \
  -H 'Content-Type: application/json' -d '{"direction":"both","delay_ms":300}'
```
Body is FLAT (`direction` + `delay_ms`/`loss_pct`/...); use `direction:both`
— the failover demo does not work one-sided.

```sh
traceroute as150.scion       # SAME hops, one jumps to ~335ms — BGP is latency-blind
hev3 --no-ip -k 3 https://web.scion/   # #p1 cancelled, #p2 (via AS151) WINS
```
The failover demo needs `--no-ip` (with IP legs present, family
interleaving parks #p2 in slot 3 and the race ends first).

## 3. Failure: BGP reconverges (~6s), SCION shrugs

```sh
curl -su scion:<booth> -X PUT http://10.20.3.200:8080/api/links/150-154/shaping \
  -H 'Content-Type: application/json' -d '{"direction":"both","loss_pct":100}'
```
Watch: map BGP badge → red in ~5s; `traceroute as150.scion` → NEW path via
`as151.scion`; `hev3 https://web.scion/` still answers throughout.
Narration: BFD detects in ~2s (interval 500ms ×4). **≥30% loss flapping the
BGP session is intended** — "BGP loses its control plane under heavy loss."

## 4. Reset (ALWAYS after demos)

Dashboard per-link reset, or:
```sh
curl -su scion:<booth> -X POST http://10.20.3.200:8080/api/links/150-154/shaping/../reset \
  -H 'Content-Type: application/json' -d '{"direction":"both"}'
```
(i.e. `POST /api/links/150-154/reset`). Badge shows `degraded` (FLAP) for
60s after recovery, then `up` — expected, not broken.

## 5. Attendee laptop (WireGuard)

Join page → claim conf → `hev3 --ca <ca.pem> https://web.scion/` races
v6-vs-v4 over the fabric (~30ms; no SCION rows without a local SCION
stack). `ping as150.scion`, `traceroute as153.scion`, DNS all work through
the tunnel. Linux `wg-quick` needs `resolvconf` for the DNS line
(NetworkManager / phone apps handle it natively).

## 6. Packet-level proof (both planes, one wire)

On any AS container (staff): `sudo tcpdump -ni sciF 'not udp port 50000'`
→ BFD + fabric ICMP; drop the filter → SCION underlay floods the capture.
On playground shells tcpdump works without root (`tcpdump -ni eth0 icmp`).

## 7. Two planes on the wall (BGP path overlay)

Open the ID-INT trace panel, pick 1-158 → 1-150, start a trace (AUTO). The
map now shows BOTH planes for the pair: brass marching dash = SCION's probed
path, **ice-blue static dash + BGP chip = BIRD's current best route** (from
each AS's live route table — no probe traffic). Panel shows the AS path
(`158 › 154 › 150`) and a SHOW ON MAP toggle.

- Run demo 2 (300ms shape): SCION polyline moves off the shaped link in
  ~30–60s (beacon re-advertisement); the BGP polyline does NOT move — the
  latency-blindness is now visible, not just narrated.
- Run demo 3 (100% loss): the BGP polyline flips to the detour (via AS151)
  within ~10s (BFD ~2s + 5s poll) while the badge goes red.
- If a mid-path linkd is unreachable the polyline dims and truncates and the
  panel shows `158 › 154 › ?` — honest partial data, not an error.

The overlay only renders while a trace is running. Reset per demo 4.

## 8. Shared fate: congestion hits both planes, only SCION escapes (staff-run)

One real queue, no simulation: BGP bulk traffic and SCION contend on the SAME
emulated link. All numbers below verified live 2026-07-11.

```sh
# staff shell — server (NO systemd unit on purpose; kill it after):
ssh ietf@10.20.3.215 'iperf3 -s -B 10.150.0.80 -D'
# sharpener — cap the demo link so saturation is instant:
curl -su scion:<booth> -X PUT http://10.20.3.200:8080/api/links/150-154/shaping \
  -H 'Content-Type: application/json' -d '{"direction":"both","rate_mbit":25}'
# load (playground shell, guests can run this themselves):
iperf3 -c 10.150.0.80 -t 30        # holds ~23 Mbit/s, 0 retransmits — the queue absorbs it
```

While the load runs (second playground shell):
```sh
traceroute 10.150.0.80                       # last hop 44ms -> 1000-1600ms. Bufferbloat, live.
scion ping --sequence "1-158 1-154 1-150" 1-150,10.20.3.215 -c 4   # PINNED: ~650-800ms
scion ping 1-150,10.20.3.215 -c 4            # free choice: ~47ms — routed AROUND via 151/153
hev3 --no-ip -k 3 https://web.scion/         # same escape, as a page load
```

Narration: the pinned ping proves neither plane is privileged — same wire,
same queue (~700ms for both). But SCION *clients pick paths*: shaping rewrote
the link's advertised bandwidth (linkd shape-sync), beacons carried it, and
sciond's first path now avoids the congested link — 47ms while BGP sits in a
1.6-second queue it can never leave: **no loss ⇒ no BFD ⇒ no reroute. BGP is
congestion-blind; the badge stays green while the band goes critical.**
Dashboard: link 150-154 band flips to critical in ~5s; BGP badge stays UP.

Gotchas: SCION ping targets the **mgmt addr** (`10.20.3.215`) — the fabric
`.81` addr has no SCMP responder. Plain `ping` lacks cap_net_raw on the
playground — use `traceroute` for IP RTT.

RESET after: link reset (demo 4) AND `ssh ietf@10.20.3.215 'pkill iperf3'`.

## Cross-checks when something looks off

- `bash tools/booth-check.sh` from the dev box — sessions, shapes, health, smoke.
- Sessions on one AS: `curl http://10.20.3.15x:30480/api/v1/bgp` (from mgmt).
- All-links state: dashboard map; grey BGP badge = linkd/BIRD unreachable, red = session down.
- sciond path-fetch crossing a shaped link makes the FIRST hev3 run slow (~700ms start) — feature, mention it or rerun.
