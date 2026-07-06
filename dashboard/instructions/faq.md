# FAQ

**What is this?** A 12-AS SCION network built for IETF 126, running as real
SCION control/data plane software (border routers, control services,
sciond) across containers, with a live topology dashboard. Attendees can
join as real endhosts in ASes 1-158..1-161 (laptop over WireGuard, or a
zero-install browser terminal), and can also just watch the map.

**What's a "SCION link"?** Each line on the dashboard map is an inter-AS
link the border routers actually forward traffic over — not a simulated
edge. When you run `scion showpaths`/`ping`/`traceroute`, the path you get
back is one or more of those real links end to end, and the map animates
the packets you actually send. Link "shaping" controls on the dashboard
(latency/bandwidth/jitter/loss, staff-only) apply `tc` on those same
interfaces, so a shaped link changes real RTT you can measure from your
own `ping`.

**Can I just download prebuilt SCION binaries?** No — the official
scionproto release binaries (including `v0.15.0`) are built
`CGO_ENABLED=0`, and `v0.15.0` reverted to the mattn/go-sqlite3 driver,
which requires cgo for the trust/path DBs. The result: the official
`scion-daemon` panics on startup (`go-sqlite3 ... is a stub`) and never
gets to open a socket. Build from source instead with `CGO_ENABLED=1` (see
`laptop-linux.md`/`laptop-macos.md`) — we've proven upstream `v0.15.0`
built this way interoperates with this testbed. If the booth is offering a
prebuilt CGO-enabled binary for your platform, that works too.

**How big a payload can I send through my WireGuard tunnel?** Keep
tunnelled SCION payloads under **~1200 bytes**. Your WG interface reports an
MTU of 1380, and SCION's path metadata for these testbed links advertises
up to 1452 bytes — but the real ceiling your packets have to clear is the
*tunnel's* effective MTU once SCION and WireGuard headers are accounted
for, which is well under either of those numbers. Don't pass `--max-mtu` to
`scion ping`/`traceroute`, and don't run bulk transfers over the tunnel:
oversized payloads fail cleanly (a timeout or a clear error), they don't
silently corrupt — but they also won't get through, so there's no point
pushing past ~1200 B for anything you're doing at the booth.

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

**The hub looks offline / my conf claim fails with a connection error.**
The join page's "Join with your laptop" section shows a hub-offline banner
when the WireGuard hub (CT201) isn't reachable — the browser terminals
(playground) are unaffected since they don't go through the hub at all.
Check back in a few minutes, or ask at the booth; staff can see the same
status.

**I lost my conf / closed the tab / switched laptops.** The join page keeps
your last claimed conf in your browser's local storage, so revisiting
`/join` on the *same* browser re-shows it (including the QR code and
download button) without claiming a new one. If that's not available
(different browser, cleared storage, different laptop), just claim again —
50 confs are provisioned and claims aren't rationed per person, so a second
claim is fine. Note claiming again gives you a *different* slot/IP; the old
one still exists until staff revoke it.

**All the confs are claimed (409 / "no confs left").** All 50 slots are in
use. Ask at the booth — staff can free up conf slots that are no longer in
use (attendees who left, etc.).

**Can I run this after I leave the venue?** No — the WireGuard endpoint is
only reachable from the venue network, and the whole testbed is torn down
after the event.
