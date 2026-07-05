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
