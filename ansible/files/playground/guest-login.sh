#!/bin/bash
# Entry point ttyd spawns. Resets the guest home, then drops into a shell
# confined to guest.slice as the guest user.
set -euo pipefail
rm -rf /home/guest/* /home/guest/.[!.]* 2>/dev/null || true
# cd "$HOME" inside the scope: --scope inherits ttyd's cwd (/) and neither
# systemd-run nor `bash --login` chdirs to the home dir like a real login.
exec systemd-run --quiet --scope --slice=guest.slice \
  --uid=guest --gid=guest /bin/bash -c 'cd "$HOME" && exec /bin/bash --login'
