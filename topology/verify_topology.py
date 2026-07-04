#!/usr/bin/env python3
"""Verify config/AS*/topology.json links are reciprocal and consistent
with proxmox/create_contianers.sh bridge/IP assignments."""
import json, glob, re, sys, os

root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
fail = []

# 1. Collect every BR interface: (as, ifid) -> (local, remote, neighbor)
ifs = {}
for f in sorted(glob.glob(os.path.join(root, "config/AS*/topology.json"))):
    t = json.load(open(f))
    asnum = int(t["isd_as"].split("-")[1])
    for br in t["border_routers"].values():
        for ifid, ic in br["interfaces"].items():
            loc = ic["underlay"]["local"]
            rem = ic["underlay"]["remote"]
            nbr = int(ic["isd_as"].split("-")[1])
            ifs[(asnum, ifid)] = (loc, rem, nbr)

def subnet(addr):  # "[fd00:fade:9::155]:50000" -> "9"
    return re.search(r"fd00:fade:([0-9a-f]+)::", addr).group(1)

# 2. Every interface's local/remote must share a /64, and the neighbor AS
#    must have exactly one interface whose local == our remote.
links = set()
for (asnum, ifid), (loc, rem, nbr) in ifs.items():
    if subnet(loc) != subnet(rem):
        fail.append(f"AS{asnum} if{ifid}: local {loc} and remote {rem} in different subnets")
        continue
    peers = [(a, i) for (a, i), (l, r, n) in ifs.items()
             if a == nbr and l.split("]")[0] == rem.split("]")[0]]
    if len(peers) != 1:
        fail.append(f"AS{asnum} if{ifid}: remote {rem} matched {len(peers)} interfaces on AS{nbr}")
        continue
    links.add(frozenset([(asnum, subnet(loc)), (nbr, subnet(loc))]))

# 3. Container script must give each AS exactly the subnets its config uses.
want = {}
for (asnum, _), (loc, _, _) in ifs.items():
    want.setdefault(asnum, set()).add(subnet(loc))
have, cur = {}, None
for line in open(os.path.join(root, "proxmox/create_contianers.sh")):
    m = re.search(r"pct create (\d+)", line)
    if m: cur = int(m.group(1))
    m = re.search(r"ip6=fd00:fade:([0-9a-fA-F]+)::", line)
    if m and cur and cur >= 150: have.setdefault(cur, set()).add(m.group(1).lower())
for asnum in sorted(want):
    if want[asnum] != have.get(asnum, set()):
        fail.append(f"AS{asnum}: config subnets {sorted(want[asnum])} != container {sorted(have.get(asnum, set()))}")

# 4. Prometheus addresses must live on the AS's own management IP.
for f in sorted(glob.glob(os.path.join(root, "config/AS*/*.toml"))):
    asnum = int(re.search(r"AS(\d+)", f).group(1))
    for line in open(f):
        m = re.search(r'prometheus = "([\d.]+):\d+"', line)
        if m and m.group(1) != f"10.20.3.{asnum}":
            fail.append(f"{os.path.relpath(f, root)}: prometheus addr {m.group(1)} != 10.20.3.{asnum}")

# 5. Control/discovery service and BR internal addresses must live on the
#    AS's own management IP.
for f in sorted(glob.glob(os.path.join(root, "config/AS*/topology.json"))):
    asnum = int(re.search(r"AS(\d+)", f).group(1))
    t = json.load(open(f))
    addrs = []
    for svc in ("control_service", "discovery_service"):
        for name, entry in t.get(svc, {}).items():
            addrs.append((f"{svc}.{name}.addr", entry["addr"]))
    for name, br in t.get("border_routers", {}).items():
        addrs.append((f"border_routers.{name}.internal_addr", br["internal_addr"]))
    for where, addr in addrs:
        host = addr.rsplit(":", 1)[0]
        if host != f"10.20.3.{asnum}":
            fail.append(f"{os.path.relpath(f, root)}: {where} host {host} != 10.20.3.{asnum}")

if fail:
    print("\n".join(fail)); sys.exit(1)
print(f"OK: {len(links)} links consistent")
