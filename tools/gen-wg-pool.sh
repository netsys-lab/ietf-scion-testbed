#!/bin/bash
# Generate the wg-hub server config + 50-slot attendee conf pool.
# Output: .build/wghub/{wg0.conf,pool.json}. Idempotent only by
# regeneration — rerunning REPLACES all keys (existing attendee confs die);
# it refuses to overwrite unless -f is given.
set -euo pipefail

OUT="$(cd "$(dirname "$0")/.." && pwd)/.build/wghub"
[ -e "$OUT/pool.json" ] && [ "${1:-}" != "-f" ] && {
  echo "refusing to overwrite existing $OUT/pool.json (use -f)"; exit 1; }
mkdir -p "$OUT"; umask 077

SERVER_PRIV=$(wg genkey); SERVER_PUB=$(printf %s "$SERVER_PRIV" | wg pubkey)

{
  echo "[Interface]"
  echo "Address = 10.20.5.1/24"
  echo "ListenPort = 51820"
  echo "PrivateKey = $SERVER_PRIV"
  echo "MTU = 1420"
  # ingress police ≈200Mbit on attendee traffic entering via wg0
  echo "PostUp = tc qdisc add dev wg0 handle ffff: ingress || true"
  echo "PostUp = tc filter add dev wg0 parent ffff: matchall action police rate 200mbit burst 1m drop || true"
} > "$OUT/wg0.conf"

echo -n '{"server_public_key": "'"$SERVER_PUB"'", "listen_port": 51820, "slots": [' > "$OUT/pool.json"
for n in $(seq 2 51); do
  PRIV=$(wg genkey); PUB=$(printf %s "$PRIV" | wg pubkey)
  printf '\n\n[Peer]\nPublicKey = %s\nAllowedIPs = 10.20.5.%d/32\n' "$PUB" "$n" >> "$OUT/wg0.conf"
  [ "$n" -gt 2 ] && echo -n ', ' >> "$OUT/pool.json"
  echo -n '{"n": '"$n"', "ip": "10.20.5.'"$n"'", "private_key": "'"$PRIV"'", "public_key": "'"$PUB"'"}' >> "$OUT/pool.json"
done
echo ']}' >> "$OUT/pool.json"

echo "wrote $OUT/wg0.conf ($(grep -c '^\[Peer\]' "$OUT/wg0.conf") peers) and $OUT/pool.json"
