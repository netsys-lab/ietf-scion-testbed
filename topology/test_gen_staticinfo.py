#!/usr/bin/env python3
"""Unit tests for gen_staticinfo.py. Run: python3 -m unittest test_gen_staticinfo -v"""
import glob, json, os, re, sys, tempfile, unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import gen_staticinfo as g

VIENNA = (48.2082, 16.3738)
FRANKFURT = (50.1109, 8.6821)
MADRID = (40.4168, -3.7038)
MODEL = {"speed_km_per_s": 200000, "floor_ms": 1.0, "round_ms": 0.1}


class TestLatencyModel(unittest.TestCase):
    def test_floor_applies_for_same_city(self):
        self.assertEqual(g.story_latency_ms(VIENNA, VIENNA, MODEL), 1.0)

    def test_symmetric(self):
        self.assertEqual(g.story_latency_ms(VIENNA, FRANKFURT, MODEL),
                         g.story_latency_ms(FRANKFURT, VIENNA, MODEL))

    def test_vienna_frankfurt_plausible(self):
        # ~600 km great-circle -> ~3.0 ms one-way at 200,000 km/s
        ms = g.story_latency_ms(VIENNA, FRANKFURT, MODEL)
        self.assertGreaterEqual(ms, 2.5)
        self.assertLessEqual(ms, 3.5)

    def test_monotonic_with_distance(self):
        self.assertGreater(g.story_latency_ms(VIENNA, MADRID, MODEL),
                           g.story_latency_ms(VIENNA, FRANKFURT, MODEL))

    def test_rounded_to_tenth(self):
        ms = g.story_latency_ms(VIENNA, MADRID, MODEL)
        self.assertAlmostEqual(ms * 10, round(ms * 10), places=6)


class TestGenerate(unittest.TestCase):
    """Golden test on a synthetic 2-AS topology."""

    def setUp(self):
        self.dir = tempfile.TemporaryDirectory()
        root = self.dir.name
        os.makedirs(os.path.join(root, "config/AS150"))
        os.makedirs(os.path.join(root, "config/AS152"))
        os.makedirs(os.path.join(root, "topology"))
        topo150 = {"isd_as": "1-150", "border_routers": {"br1-150-1": {"interfaces": {
            "18982": {"underlay": {"local": "[fd00:fade:2::150]:50000",
                                   "remote": "[fd00:fade:2::152]:50000"},
                      "isd_as": "1-152", "link_to": "core"},
            "20879": {"underlay": {"local": "[fd00:fade:3::150]:50000",
                                   "remote": "[fd00:fade:3::152]:50000"},
                      "isd_as": "1-152", "link_to": "core"}}}}}
        topo152 = {"isd_as": "1-152", "border_routers": {"br1-152-1": {"interfaces": {
            "6957": {"underlay": {"local": "[fd00:fade:2::152]:50000",
                                  "remote": "[fd00:fade:2::150]:50000"},
                     "isd_as": "1-150", "link_to": "core"},
            "43214": {"underlay": {"local": "[fd00:fade:3::152]:50000",
                                   "remote": "[fd00:fade:3::150]:50000"},
                      "isd_as": "1-150", "link_to": "core"}}}}}
        json.dump(topo150, open(os.path.join(root, "config/AS150/topology.json"), "w"))
        json.dump(topo152, open(os.path.join(root, "config/AS152/topology.json"), "w"))
        self.story = {
            "model": dict(MODEL),
            "tiers_mbit": {"core": 10000, "parent_child": 1000, "peer": 500},
            "intra": {"latency_ms": 0.0, "bandwidth_mbit": 100000, "hops": 0},
            "note": "test AS at {city}",
            "ases": {150: {"city": "Vienna", "lat": VIENNA[0], "lon": VIENNA[1]},
                     152: {"city": "Frankfurt", "lat": FRANKFURT[0], "lon": FRANKFURT[1]}},
            "overrides": {"150-152": {"latency_ms": 9.9}},
        }
        self.root = root

    def tearDown(self):
        self.dir.cleanup()

    def out(self, asnum, name):
        return json.load(open(os.path.join(self.root, f"config/AS{asnum}/{name}")))

    def test_generate_files(self):
        files = g.generate(self.root, self.story)
        g.write_files(files)
        base = self.out(150, "staticInfoConfig.base.json")
        # override wins over the model on both links of the pair
        # (integer microseconds: the deployed CS duration parser is
        # integer-only, so 9.9ms must round-trip as "9900us")
        self.assertEqual(base["Latency"]["18982"]["Inter"], "9900us")
        self.assertEqual(base["Latency"]["20879"]["Inter"], "9900us")
        # Intra entry only under the numerically smaller ifid
        self.assertIn("20879", base["Latency"]["18982"]["Intra"])
        self.assertNotIn("18982", base["Latency"]["20879"].get("Intra", {}))
        # tier bandwidth in Kbit/s
        self.assertEqual(base["Bandwidth"]["18982"]["Inter"], 10000 * 1000)
        self.assertEqual(base["Bandwidth"]["18982"]["Intra"]["20879"], 100000 * 1000)
        self.assertEqual(base["LinkType"]["18982"], "direct")
        self.assertEqual(base["Geo"]["18982"]["Address"], "Vienna")
        self.assertAlmostEqual(base["Geo"]["18982"]["Latitude"], VIENNA[0])
        self.assertEqual(base["Hops"]["18982"]["Intra"]["20879"], 0)
        self.assertEqual(base["Note"], "test AS at Vienna")
        # baseline profile: same one-way latency as delay, tier as rate
        bl = self.out(150, "linkd-baseline.json")
        self.assertEqual(bl["18982"], {"delay_ms": 9.9, "rate_mbit": 10000})
        # peer file mirrors the story
        base152 = self.out(152, "staticInfoConfig.base.json")
        self.assertEqual(base152["Latency"]["6957"]["Inter"], "9900us")
        self.assertEqual(base152["Geo"]["6957"]["Address"], "Frankfurt")

    def test_unknown_as_fails(self):
        del self.story["ases"][152]
        with self.assertRaises(SystemExit):
            g.generate(self.root, self.story)


class TestDurationFormat(unittest.TestCase):
    """Round-trip guard: every emitted duration string must parse under the
    deployed CS fork's integer-only grammar (scion fork
    pkg/private/util/duration.go), never a fractional value like "1.3ms".
    Exercises the real repo's committed generated files, not a synthetic
    fixture, so a regression here is caught before it reaches config/.
    """

    DURATION_RE = re.compile(r"^-?[0-9]+(y|w|d|h|m|s|ms|us|\xb5s|ns)$")

    def test_all_generated_latencies_are_integer_durations(self):
        root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        files = sorted(glob.glob(os.path.join(root, "config/AS*/staticInfoConfig.base.json")))
        self.assertEqual(len(files), 12, f"expected 12 generated files, found {len(files)}: {files}")
        checked = 0
        for path in files:
            doc = json.load(open(path))
            for ifid, entry in doc["Latency"].items():
                self.assertRegex(entry["Inter"], self.DURATION_RE,
                                  f"{path}: Latency[{ifid}].Inter = {entry['Inter']!r}")
                checked += 1
                for j, v in entry.get("Intra", {}).items():
                    self.assertRegex(v, self.DURATION_RE,
                                      f"{path}: Latency[{ifid}].Intra[{j}] = {v!r}")
                    checked += 1
        self.assertGreater(checked, 0, "no Latency entries found to check")


if __name__ == "__main__":
    unittest.main()
