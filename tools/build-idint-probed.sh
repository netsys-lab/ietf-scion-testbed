#!/bin/bash
# Build idint-probed (per-AS ID-INT prober sidecar for the dashboard's
# path inspector) natively with CGO_ENABLED=1 — the scion fork's pkg/fcrypto
# is cgo-only. Same GLIBC_2.39 ceiling guard as build-idint-traceroute.sh
# (containers are Ubuntu 24.04).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/.build/idint-probed/bin"
command -v go >/dev/null || { echo "error: go not found" >&2; exit 1; }
mkdir -p "$OUT"
echo "Building idint-probed -> $OUT"
(cd "$ROOT/idint-probed" && CGO_ENABLED=1 go build -o "$OUT/idint-probed" .)
max_glibc="$(objdump -T "$OUT/idint-probed" | grep -oE 'GLIBC_[0-9]+\.[0-9]+' | sort -uV | tail -1)"
if [ "$(printf '%s\nGLIBC_2.39\n' "$max_glibc" | sort -V | tail -1)" != "GLIBC_2.39" ]; then
    echo "error: binary needs $max_glibc > GLIBC_2.39 ceiling" >&2; exit 1
fi
help_out="$("$OUT/idint-probed" -h 2>&1 || true)"
grep -q -- '-listen' <<<"$help_out" && grep -q -- '-sciond' <<<"$help_out" \
    || { echo "error: smoke test failed:"; echo "$help_out"; exit 1; } >&2
echo "Built:"; ls -la "$OUT"
