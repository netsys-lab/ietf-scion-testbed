# FAQ

**What is this?** A 12-AS SCION network built for IETF 126, running as real
SCION control/data plane software (border routers, control services,
sciond) across containers, with a live topology dashboard. Attendees can
join as real endhosts in ASes 1-152, 1-155, 1-158, 1-161 (laptop over WireGuard, or a
zero-install browser terminal), and can also just watch the map.

**What's a "SCION link"?** Each line on the dashboard map is an inter-AS
link the border routers actually forward traffic over — not a simulated
edge. When you run `scion showpaths`/`ping`/`traceroute`, the path you get
back is one or more of those real links end to end, and the map animates
the packets you actually send. Link "shaping" controls on the dashboard
(latency/bandwidth/jitter/loss, staff-only) apply `tc` on those same
interfaces, so a shaped link changes real RTT you can measure from your
own `ping`.

**What's the "BGP" badge / the `as150.scion` names?** The same inter-AS
links also carry a plain BGP/IP network (BIRD on every AS, IPv4+IPv6), so
you can compare today's Internet routing with SCION on identical
infrastructure. Each AS has an anchor address (`as150.scion` …
`as161.scion`) — `traceroute as153.scion` shows the BGP path as per-AS
hops. Link shaping applies to both planes at once (one `tc` qdisc on
the shared interface); the map's BGP badge turns red when a "link failure"
(100% loss) tears the BGP session down and the fabric reroutes — SCION, by
contrast, can fail over to another path per-flow in about one round trip.

**curl says "unable to get local issuer certificate" for `web.scion`.**
The demo servers use a throwaway testbed CA, not a public one. On the
playground shells the CA is preinstalled in the system trust store, so plain
`curl https://web.scion/` just works. From your own laptop (WireGuard), 
download the CA from the join page ("Download TLS CA") and pass
`curl --cacert scion-testbed-ca.pem https://web.scion/` — or accept `-k`
for a quick look. Never trust this CA outside the testbed; its private key
is public by design — stick to `--cacert`, don't install it system-wide.

**How big a payload can I send through my WireGuard tunnel?** Keep
tunnelled SCION payloads under **~1200 bytes**. Your WG interface reports an
MTU of 1380, and SCION's path metadata for these testbed links advertises
up to 1452 bytes — but the real ceiling your packets have to clear is the
*tunnel's* effective MTU once SCION and WireGuard headers are accounted
for, which is well under either of those numbers.

**Why is my `fc00...` identity different when I ping into another AS?**
The `fc00::/8` address scitra shows you (and that you `ping -6` from a
playground shell) encodes the *ISD and AS number of the endhost it names*,
not just your IP — the mapping is `fc00 | ISD | ASN | ... | your IPv4`. So
your own identity's prefix changes if you re-home your conf to a different
AS (same tunnel IP, different `fc00...` prefix), and every other AS's
endhosts have their own prefix too, e.g. AS 1-158's playground box is
`fc00:1000:9e00::ffff:10.20.3.210` while AS 1-161's is
`fc00:1000:a100::ffff:10.20.3.213` — same testbed, different AS number
baked into the address. This is expected: the address *is* "which AS, which
host," not a stable per-attendee identity.
