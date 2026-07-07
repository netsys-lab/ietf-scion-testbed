#!/bin/bash
# Build the scion-apps binaries shipped to the playground endhosts:
# the bandwidth tester (client + server), netcat, and the curl-like HTTP/QUIC
# client (bat). scion-apps builds all apps with `make` into ./bin; we copy the
# four we deploy.
#
# Build on a host whose glibc is <= the fleet's (Ubuntu 24.04, glibc 2.39) so
# the binaries load on the containers. These are pure-Go apps (no cgo), so a
# newer build host is usually fine too, but keep the fleet ceiling in mind.
#
# USAGE:  ./tools/build-scion-apps.sh   # -> .build/scion-apps/bin/{scion-bwtestclient,scion-bwtestserver,scion-netcat,scion-bat}
# Env vars:
#   SCION_APPS_REPO  git URL (default https://github.com/netsec-ethz/scion-apps)
#   SCION_APPS_SRC   clone dir (default .build/src/scion-apps)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
REPO="${SCION_APPS_REPO:-https://github.com/netsec-ethz/scion-apps}"
SRC="${SCION_APPS_SRC:-$REPO_ROOT/.build/src/scion-apps}"
OUT="$REPO_ROOT/.build/scion-apps/bin"
WANT=(scion-bwtestclient scion-bwtestserver scion-netcat scion-bat)

command -v go >/dev/null || { echo "error: go not found" >&2; exit 1; }
command -v make >/dev/null || { echo "error: make not found" >&2; exit 1; }

mkdir -p "$(dirname "$SRC")" "$OUT"
if [ ! -d "$SRC/.git" ]; then
    git clone "$REPO" "$SRC"
fi
git -C "$SRC" pull --ff-only
echo "Building scion-apps in $SRC (make -j)"
( cd "$SRC" && make -j )

# scion-apps emits binaries under bin/; copy the ones we ship.
for b in "${WANT[@]}"; do
    if [ ! -x "$SRC/bin/$b" ]; then
        echo "error: expected binary '$b' not found in $SRC/bin — actual contents:" >&2
        ls -1 "$SRC/bin" >&2
        exit 1
    fi
    install -m0755 "$SRC/bin/$b" "$OUT/$b"
done
echo "Built: ${WANT[*]} -> $OUT"
ls -la "$OUT"
