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
EOF

CGO_ENABLED=0 go build -o "$TMP/scion-linkd" ./cmd/scion-linkd
ip netns exec $NS "$TMP/scion-linkd" -config "$TMP/config.toml" &
sleep 1

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

ip netns exec $NS curl -fsS -X DELETE http://127.0.0.1:30480/api/v1/links/6049 >/dev/null
ip netns exec $NS tc qdisc show dev sci9 | grep -q netem && { echo "FAIL: netem not cleared"; exit 1; }

echo PASS
