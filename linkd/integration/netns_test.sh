#!/bin/bash
# End-to-end test in a network namespace: real netlink, real netem.
# Usage: sudo ./integration/netns_test.sh
set -euo pipefail
[ "$EUID" -eq 0 ] || { echo "run with sudo"; exit 1; }
cd "$(dirname "$0")/.."

NS=linkdtest
TMP=$(mktemp -d)
cleanup() { ip netns del $NS 2>/dev/null || true; rm -rf "$TMP"; kill %1 2>/dev/null || true; }
trap cleanup EXIT

ip netns add $NS
ip -n $NS link add sci9 type veth peer name sci9p
ip -n $NS addr add fd00:fade:9::155/64 dev sci9
ip -n $NS link set sci9 up
ip -n $NS link set lo up

cat > "$TMP/topology.json" <<'EOF'
{"isd_as":"1-155","border_routers":{"br1-155-1":{"interfaces":{
"6049":{"underlay":{"local":"[fd00:fade:9::155]:50000","remote":"[fd00:fade:9::151]:50000"},
"isd_as":"1-151","link_to":"parent","mtu":1452}}}}}
EOF
cat > "$TMP/config.toml" <<EOF
listen = "127.0.0.1:30480"
topology_glob = "$TMP/topology.json"
staticinfo_base = "$TMP/staticInfoConfig.base.json"
baseline_profile = "$TMP/linkd-baseline.json"
cs_reload_unit = ""
EOF
cat > "$TMP/staticInfoConfig.base.json" <<'EOF'
{"Latency":{"6049":{"Inter":"3000us","Intra":{}}},
 "Bandwidth":{"6049":{"Inter":500000,"Intra":{}}},
 "LinkType":{"6049":"direct"},
 "Geo":{"6049":{"Latitude":52.4,"Longitude":4.9,"Address":"Amsterdam"}},
 "Hops":{"6049":{"Intra":{}}},"Note":"netns test"}
EOF
cat > "$TMP/linkd-baseline.json" <<'EOF'
{"6049": {"delay_ms": 3, "rate_mbit": 500}}
EOF

CGO_ENABLED=0 go build -o "$TMP/scion-linkd" ./cmd/scion-linkd
ip netns exec $NS "$TMP/scion-linkd" -config "$TMP/config.toml" &
sleep 1

# preshape + initial metadata: the daemon must have applied the story
# baseline to the bare interface and synced staticInfoConfig.json before
# ever seeing a PUT.
QD0=$(ip netns exec $NS tc qdisc show dev sci9)
echo "$QD0" | grep -q "delay 3ms"   || { echo "FAIL: no preshape delay: $QD0"; exit 1; }
echo "$QD0" | grep -q "rate 500Mbit" || { echo "FAIL: no preshape rate: $QD0"; exit 1; }
grep -q '"Inter": "3000us"' "$TMP/staticInfoConfig.json" || { echo "FAIL: initial metadata"; cat "$TMP/staticInfoConfig.json"; exit 1; }
LIST0=$(ip netns exec $NS curl -fsS http://127.0.0.1:30480/api/v1/links)
echo "$LIST0" | grep -q '"shaped":false' || { echo "FAIL: baseline must not count as shaped: $LIST0"; exit 1; }

# the daemon listens inside the ns, so curl must run inside it too
ip netns exec $NS curl -fsS -X PUT \
  -d '{"delay_ms":50,"jitter_ms":5,"loss_pct":1,"rate_mbit":50}' \
  http://127.0.0.1:30480/api/v1/links/6049 >/dev/null
QD=$(ip netns exec $NS tc qdisc show dev sci9)
echo "$QD" | grep -q netem       || { echo "FAIL: no netem: $QD"; exit 1; }
echo "$QD" | grep -q "delay 50ms" || { echo "FAIL: no delay: $QD"; exit 1; }
echo "$QD" | grep -q "loss 1%"    || { echo "FAIL: no loss: $QD"; exit 1; }
echo "$QD" | grep -q "rate 50Mbit" || { echo "FAIL: no rate (see Rate64 fallback note in plan Task 3): $QD"; exit 1; }

GET=$(ip netns exec $NS curl -fsS http://127.0.0.1:30480/api/v1/links)
echo "$GET" | grep -q '"delay_ms":5' || { echo "FAIL: GET does not reflect kernel delay: $GET"; exit 1; }
echo "$GET" | grep -Eq '"loss_pct":(1|0\.9[0-9]*|1\.0[0-9]*)' || { echo "FAIL: GET does not reflect kernel loss: $GET"; exit 1; }
echo "$GET" | grep -Eq '"rate_mbit":(50|49\.9[0-9]*|50\.0[0-9]*)' || { echo "FAIL: GET does not reflect kernel rate: $GET"; exit 1; }

grep -q '"Inter": "50000us"' "$TMP/staticInfoConfig.json" || { echo "FAIL: metadata not updated after PUT"; exit 1; }
# kernel rate readback is approximate (see the existing rate_mbit tolerance
# grep above): 50 Mbit may come back 49.8xx -> Kbit 49800..50000
grep -Eq '"Inter": (49[89][0-9][0-9]|50000)' "$TMP/staticInfoConfig.json" || { echo "FAIL: bandwidth not updated after PUT"; exit 1; }
echo "$GET" | grep -q '"shaped":true' || { echo "FAIL: PUT state must be shaped: $GET"; exit 1; }

ip netns exec $NS curl -fsS -X DELETE http://127.0.0.1:30480/api/v1/links/6049 >/dev/null
QD2=$(ip netns exec $NS tc qdisc show dev sci9)
echo "$QD2" | grep -q "delay 3ms" || { echo "FAIL: DELETE must restore baseline: $QD2"; exit 1; }
grep -q '"Inter": "3000us"' "$TMP/staticInfoConfig.json" || { echo "FAIL: metadata not restored: "; exit 1; }
LIST2=$(ip netns exec $NS curl -fsS http://127.0.0.1:30480/api/v1/links)
echo "$LIST2" | grep -q '"shaped":false' || { echo "FAIL: reset link still shaped: $LIST2"; exit 1; }

echo PASS
