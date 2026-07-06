#!/bin/bash
# Create the locked-down 'guest' user + a resource-capped slice. Idempotent.
set -euo pipefail

# guest user, fixed uid 2000, no password login, tmpfs home mounted by pam? no —
# home lives on the container rootfs and is wiped on each ttyd session start.
if ! id guest >/dev/null 2>&1; then
  useradd --uid 2000 --create-home --shell /bin/bash guest
fi
# No sudo, no ssh for guest.
passwd -l guest || true

# Resource cap slice for guest sessions.
install -D -m0644 /dev/stdin /etc/systemd/system/guest.slice <<'EOF'
[Slice]
CPUQuota=50%
MemoryMax=256M
TasksMax=64
EOF
systemctl daemon-reload

# Let gid 2000 (guest) open unprivileged ICMP "ping" sockets (SOCK_DGRAM),
# without granting CAP_NET_RAW. Same sysctl gates both ICMP and ICMPv6 despite
# the net.ipv4 name — needed for `ping`/`ping -6` (incl. the fc00/scitra demo)
# to work from the guest shell.
install -D -m0644 /dev/stdin /etc/sysctl.d/60-guest-ping.conf <<'EOF'
net.ipv4.ping_group_range = 2000 2000
EOF
sysctl -p /etc/sysctl.d/60-guest-ping.conf >/dev/null
