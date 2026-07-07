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

[ -z "$v4" ] && { echo "error: hub eth1 has no IPv4" >&2; exit 1; }
[ -z "$v6" ] && { echo "error: hub eth1 has no global IPv6" >&2; exit 1; }

echo "hub CT$HUB_CT eth1 -> wg_endpoint_v4=$v4  wg_endpoint_v6=$v6"

sed -i "s|wg_endpoint_v4:.*|wg_endpoint_v4: \"$v4\"|" "$GV"
sed -i "s|wg_endpoint_v6:.*|wg_endpoint_v6: \"$v6\"|" "$GV"
grep -E "wg_endpoint" "$GV"

echo "redeploying dashboard so /api/join/meta advertises the new endpoint..."
cd "$REPO"
ansible-playbook -i ansible/inventory.yaml ansible/playbooks/deploy_dashboard.yaml
