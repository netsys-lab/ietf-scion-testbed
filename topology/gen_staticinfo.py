#!/usr/bin/env python3
"""Generate per-AS beacon metadata files from topology/staticinfo.yml.

Emits, per AS, into config/AS<n>/:
  staticInfoConfig.base.json  - static StaticInfoCfg (geo/linktype/intra/note
                                + story baselines for Inter latency/bandwidth)
  linkd-baseline.json         - {ifid: {delay_ms, rate_mbit}} preshape profile
  topology.json               - rewritten in place: per-interface idint.speed
                                and per-BR idint.internal_speed derived from
                                the same story rates (bits/s — the fork BR's
                                ID-INT utilization meters divide by this;
                                router/dp_metrics.go newLinkMeter). All other
                                topology fields, incl. idint.id, pass through.

Usage: gen_staticinfo.py [--check]
  --check: regenerate in memory and diff against committed files (exit 1 on drift).
"""
import json, glob, os, sys
from math import radians, sin, cos, asin, sqrt

import yaml

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def dist_km(a, b):
    la1, lo1, la2, lo2 = map(radians, (a[0], a[1], b[0], b[1]))
    h = sin((la2 - la1) / 2) ** 2 + cos(la1) * cos(la2) * sin((lo2 - lo1) / 2) ** 2
    return 2 * 6371 * asin(sqrt(h))


def story_latency_ms(a, b, model):
    ms = dist_km(a, b) / model["speed_km_per_s"] * 1000
    ms = max(ms, model["floor_ms"])
    r = model["round_ms"]
    return round(round(ms / r) * r, 1)


def die(msg):
    print(f"gen_staticinfo: {msg}", file=sys.stderr)
    sys.exit(1)


def link_key(a, b):
    return f"{min(a, b)}-{max(a, b)}"


def tier_for(link_to, tiers):
    if link_to == "core":
        return tiers["core"]
    if link_to == "peer":
        return tiers["peer"]
    if link_to in ("parent", "child"):
        return tiers["parent_child"]
    die(f"unknown link_to {link_to!r}")


def load_interfaces(root):
    """(asnum -> {ifid: {"nbr": int, "link_to": str}}), from committed topologies."""
    out = {}
    for f in sorted(glob.glob(os.path.join(root, "config/AS*/topology.json"))):
        with open(f) as fh:
            t = json.load(fh)
        asnum = int(t["isd_as"].split("-")[1])
        ifs = {}
        for br in t["border_routers"].values():
            for ifid, ic in br["interfaces"].items():
                ifs[ifid] = {"nbr": int(ic["isd_as"].split("-")[1]),
                             "link_to": ic["link_to"]}
        out[asnum] = ifs
    return out


def generate(root, story):
    """Return {path: json-serializable} for every output file."""
    model, tiers = story["model"], story["tiers_mbit"]
    intra, ases = story["intra"], story["ases"]
    overrides = story.get("overrides") or {}
    all_ifs = load_interfaces(root)
    files = {}
    for asnum, ifs in sorted(all_ifs.items()):
        if asnum not in ases:
            die(f"AS{asnum} missing from staticinfo.yml ases")
        me = ases[asnum]
        lat, bw, ltype, geo, hops, baseline = {}, {}, {}, {}, {}, {}
        for ifid, ic in sorted(ifs.items(), key=lambda kv: int(kv[0])):
            nbr = ic["nbr"]
            if nbr not in ases:
                die(f"AS{asnum} if {ifid}: neighbor AS{nbr} missing from staticinfo.yml")
            ov = overrides.get(link_key(asnum, nbr), {})
            ms = ov.get("latency_ms", story_latency_ms(
                (me["lat"], me["lon"]),
                (ases[nbr]["lat"], ases[nbr]["lon"]), model))
            mbit = ov.get("bandwidth_mbit", tier_for(ic["link_to"], tiers))
            # Deployed CS duration parser (scion fork pkg/private/util/duration.go)
            # is integer-only (strconv.Atoi): emit integer microseconds, never
            # fractional milliseconds, or json.Unmarshal aborts on the whole doc.
            fmt = lambda v: f"{round(v * 1000)}us"
            # Intra entries once, under the numerically smaller ifid.
            smaller = {j: None for j in ifs if int(j) > int(ifid)}
            lat[ifid] = {"Inter": fmt(ms),
                         "Intra": {j: fmt(intra["latency_ms"]) for j in smaller}}
            bw[ifid] = {"Inter": int(mbit * 1000),
                        "Intra": {j: intra["bandwidth_mbit"] * 1000 for j in smaller}}
            ltype[ifid] = "direct"
            geo[ifid] = {"Latitude": me["lat"], "Longitude": me["lon"],
                         "Address": me["city"]}
            hops[ifid] = {"Intra": {j: intra["hops"] for j in smaller}}
            baseline[ifid] = {"delay_ms": ms, "rate_mbit": mbit}
        base = {"Latency": lat, "Bandwidth": bw, "LinkType": ltype,
                "Geo": geo, "Hops": hops,
                "Note": story["note"].format(city=me["city"])}
        d = os.path.join(root, f"config/AS{asnum}")
        files[os.path.join(d, "staticInfoConfig.base.json")] = base
        files[os.path.join(d, "linkd-baseline.json")] = baseline

        # Refresh the committed topology.json's ID-INT speeds from the same
        # story rates that feed linkd-baseline.json (baseline shaping): the
        # BR utilization meters read these as bits/s, so a stale/mis-scaled
        # value makes ID-INT egress_link_tx read 100% on idle links.
        tpath = os.path.join(d, "topology.json")
        with open(tpath) as fh:
            topo = json.load(fh)
        for br in topo["border_routers"].values():
            br.setdefault("idint", {})["internal_speed"] = \
                intra["bandwidth_mbit"] * 10**6
            for ifid, ic in br["interfaces"].items():
                ic.setdefault("idint", {})["speed"] = \
                    baseline[ifid]["rate_mbit"] * 10**6
        files[tpath] = topo
    return {p: render(p, obj) for p, obj in files.items()}


def render(path, obj):
    # topology.json keeps its committed style (indent=2, insertion order);
    # the generated metadata files keep theirs (indent=1, sorted keys).
    if os.path.basename(path) == "topology.json":
        return json.dumps(obj, indent=2) + "\n"
    return json.dumps(obj, indent=1, sort_keys=True) + "\n"


def write_files(files):
    for path, text in files.items():
        with open(path, "w") as f:
            f.write(text)


def main():
    with open(os.path.join(ROOT, "topology/staticinfo.yml")) as fh:
        story = yaml.safe_load(fh)
    files = generate(ROOT, story)
    nas = len(files) // 3
    if "--check" in sys.argv[1:]:
        def stale(p, text):
            if not os.path.exists(p):
                return True
            with open(p) as fh:
                return fh.read() != text
        drift = [p for p, text in sorted(files.items()) if stale(p, text)]
        if drift:
            for p in drift:
                print(f"DRIFT: {os.path.relpath(p, ROOT)}", file=sys.stderr)
            sys.exit(1)
        print(f"OK: {nas} ASes generated, files match")
    else:
        write_files(files)
        print(f"OK: {nas} ASes generated, files match")


if __name__ == "__main__":
    main()
