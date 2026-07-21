#!/bin/bash
# Build the SCION endhost stack (scion CLI, sciond, shim dispatcher) from the
# deployed fork, natively for linux-amd64. NOT for cross-compilation — the
# fork's pkg/fcrypto is amd64-cgo only.
set -euo pipefail

SCION_FORK="${SCION_FORK:-$HOME/scion}"
OUT="$(cd "$(dirname "$0")/.." && pwd)/.build/endhost/bin"
mkdir -p "$OUT"

echo "Building endhost binaries from $SCION_FORK -> $OUT"
(cd "$SCION_FORK" && CGO_ENABLED=1 go build -o "$OUT/scion"           ./scion/cmd/scion)
(cd "$SCION_FORK" && CGO_ENABLED=1 go build -o "$OUT/sciond"          ./daemon/cmd/daemon)
(cd "$SCION_FORK" && CGO_ENABLED=1 go build -o "$OUT/shim-dispatcher" ./dispatcher/cmd/dispatcher)

echo "Built:"
ls -la "$OUT"
