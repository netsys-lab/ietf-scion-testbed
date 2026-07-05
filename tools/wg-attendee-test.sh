#!/bin/bash
# Simulate one attendee in a netns: wg tunnel -> sciond in AS158 -> scion ping.
# Run ON the proxmox host: sudo ./wg-attendee-test.sh <hub-endpoint-ip>
# (hub venue v4 in wired/home mode; also accepts [v6] literal).
set -euo pipefail
EP="${1:?usage: wg-attendee-test.sh <hub-endpoint>}"
NS=wgtest
POOL=/root/ietf-scion-testbed/.build/wghub/pool.json
BIN=/root/ietf-scion-testbed/.build/endhost/bin
WORK=/tmp/wgtest; rm -rf "$WORK"; mkdir -p "$WORK"

PRIV=$(python3 -c "import json;print(json.load(open('$POOL'))['slots'][0]['private_key'])")
SPUB=$(python3 -c "import json;print(json.load(open('$POOL'))['server_public_key'])")

ip netns del $NS 2>/dev/null || true
ip netns add $NS
ip link add wgt type wireguard
ip link set wgt netns $NS
ip netns exec $NS bash -c "
  printf '%s' '$PRIV' > $WORK/key; chmod 600 $WORK/key
  wg set wgt private-key $WORK/key peer $SPUB allowed-ips 10.20.3.0/24,10.20.5.0/24 endpoint $EP:51820 persistent-keepalive 25
  ip addr add 10.20.5.2/32 dev wgt
  ip link set wgt mtu 1380 up
  ip link set lo up
  ip route add 10.20.3.0/24 dev wgt
  ip route add 10.20.5.0/24 dev wgt
  ping -c 2 -W 3 10.20.5.1"
echo "TUNNEL OK"

# endhost kit for AS158 (mirrors the fabricd bundle layout)
cp /root/ietf-scion-testbed/config/AS158/topology.json "$WORK/"
mkdir -p "$WORK/certs"
cp /root/ietf-scion-testbed/config/AS158/certs/ISD1-B1-S1.trc "$WORK/certs/"
cat > "$WORK/sd.toml" <<EOF
[general]
id = "sd1-158"
config_dir = "$WORK"
[trust_db]
connection = "$WORK/sd.trust.db"
[path_db]
connection = "$WORK/sd.path.db"
[sd]
address = "127.0.0.1:30255"
[features]
experimental_idint = true
[drkey_level2_db]
connection = "$WORK/sd.drkey.db"
[log.console]
level = "info"
EOF

ip netns exec $NS $BIN/sciond --config "$WORK/sd.toml" &>"$WORK/sciond.log" &
SD=$!; sleep 4
ip netns exec $NS $BIN/scion showpaths 1-160 --sciond 127.0.0.1:30255 --maxpaths 1
ip netns exec $NS $BIN/scion ping 1-161,10.20.3.213 --sciond 127.0.0.1:30255 -c 3
# Negative check: hub pins attachment to ASes 158-161. TCP gives a decisive
# signal (connect vs timeout); UDP scans cannot distinguish drop from open.
if ip netns exec $NS timeout 3 bash -c "echo > /dev/tcp/10.20.3.158/31032"; then
  echo "PINNING: AS158 CS reachable (expected)"
else
  echo "PINNING FAIL: AS158 CS unreachable"; exit 1
fi
if ip netns exec $NS timeout 3 bash -c "echo > /dev/tcp/10.20.3.150/31000" 2>/dev/null; then
  echo "PINNING FAIL: AS150 CS reachable through the hub"; exit 1
else
  echo "PINNING: AS150 blocked (expected)"
fi

kill $SD
echo "ATTENDEE E2E PASS"
ip netns del $NS
