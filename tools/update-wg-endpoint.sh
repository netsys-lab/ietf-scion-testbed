#!/bin/bash
# Derive the WireGuard join endpoint from the hub's CURRENT eth1 address and
# push it to the dashboard. Run this on the Proxmox host AFTER the hub (CT201)
# connects to the venue network and gets its real address — the hub leg is
# DHCP/SLAAC, so its address changes on every rebuild or lease renewal and the
# join bundle's advertised endpoint would otherwise go stale (attendees'
# wg-quick up would hit the old IP and never handshake).
#
# The IETF meeting network is IPv6-mostly, so the v6 endpoint is the one
# attendees use; v4 is kept as a fallback for v4-only clients. Preference for
# v6: a global-unicast address (2000::/3 — the real venue prefix) over a ULA
# (fc00::/7, what the lab hands out), and never a temporary/privacy address
# (a server endpoint must be stable).
#
# USAGE (on ietf-proxmox):  bash tools/update-wg-endpoint.sh
set -euo pipefail

REPO="${REPO:-/root/ietf-scion-testbed}"
HUB_CT="${HUB_CT:-201}"
GV="$REPO/ansible/group_vars/playground.yml"

v4="$(pct exec "$HUB_CT" -- ip -4 -o addr show eth1 | awk '{print $4}' | cut -d/ -f1 | head -1)"

# Non-temporary global v6; prefer global-unicast (2/3), fall back to ULA.
mapfile -t g6 < <(pct exec "$HUB_CT" -- ip -6 -o addr show eth1 scope global \
                    | grep -v temporary | awk '{print $4}' | cut -d/ -f1)
v6=""
for a in "${g6[@]}"; do case "$a" in 2*|3*) v6="$a"; break;; esac; done
[ -z "$v6" ] && v6="${g6[0]:-}"

# AUTO=1 (timer/boot use): soft-skip when the hub isn't ready yet (still
# booting / hasn't reached the venue net), so a periodic run doesn't flag a
# failed unit — the next tick retries. Manual runs error hard (exit 1).
notready() { echo "$1" >&2; [ "${AUTO:-0}" = 1 ] && exit 0 || exit 1; }
[ -z "$v4" ] && notready "hub eth1 has no IPv4 yet"
[ -z "$v6" ] && notready "hub eth1 has no global IPv6 yet"

echo "hub CT$HUB_CT eth1 -> wg_endpoint_v4=$v4  wg_endpoint_v6=$v6"

# Idempotence guard: skip the (heavy) redeploy when the endpoint is unchanged,
# so this is cheap to run on every boot / from a periodic timer. FORCE=1 to
# redeploy regardless (e.g. after editing the join surface by hand).
cur_v4="$(grep -oP 'wg_endpoint_v4:\s*"\K[^"]+' "$GV" || true)"
cur_v6="$(grep -oP 'wg_endpoint_v6:\s*"\K[^"]+' "$GV" || true)"
if [ "$cur_v4" = "$v4" ] && [ "$cur_v6" = "$v6" ] && [ "${FORCE:-0}" != "1" ]; then
    echo "endpoint unchanged — nothing to do"
    exit 0
fi

echo "endpoint changed (was v4=$cur_v4 v6=$cur_v6) — updating group_vars + redeploying"
sed -i "s|wg_endpoint_v4:.*|wg_endpoint_v4: \"$v4\"|" "$GV"
sed -i "s|wg_endpoint_v6:.*|wg_endpoint_v6: \"$v6\"|" "$GV"
grep -E "wg_endpoint" "$GV"

echo "redeploying dashboard so /api/join/meta advertises the new endpoint..."
cd "$REPO"
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_dashboard.yaml
