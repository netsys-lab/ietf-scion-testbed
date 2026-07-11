#!/bin/bash
# Build CoreDNS (netsys-lab/lschulz scion-dev fork: serves the scion. TLD via
# the vendored scitra plugin + SCION SVCB records) from the local checkout at
# /home/tony/tjohn327/coredns, branch scion-dev. Fully static (CGO_ENABLED=0),
# so unlike the idint-* binaries there is no GLIBC ceiling guard to check.
set -euo pipefail

COREDNS_SRC="${COREDNS_SRC:-/home/tony/tjohn327/coredns}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/.build/coredns/bin"

if [ ! -d "$COREDNS_SRC/.git" ]; then
    echo "error: COREDNS_SRC='$COREDNS_SRC' is not a git checkout of the coredns fork" >&2
    exit 1
fi
command -v go >/dev/null || { echo "error: go not found" >&2; exit 1; }

mkdir -p "$OUT"
echo "Building coredns -> $OUT"
(cd "$COREDNS_SRC" && CGO_ENABLED=0 go build -o "$OUT/coredns" .)

echo "Version:"
"$OUT/coredns" -version

echo "Built:"; ls -la "$OUT"
