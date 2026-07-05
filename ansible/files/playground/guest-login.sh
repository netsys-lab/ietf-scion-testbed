#!/bin/bash
# Entry point ttyd spawns. Resets the guest home, then drops into a shell
# confined to guest.slice as the guest user.
set -euo pipefail
rm -rf /home/guest/* /home/guest/.[!.]* 2>/dev/null || true
exec systemd-run --quiet --scope --slice=guest.slice \
  --uid=guest --gid=guest /bin/bash --login
