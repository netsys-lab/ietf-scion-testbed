#!/bin/bash
# Build idint-traceroute (ID-INT telemetry traceroute/debug tool,
# github.com/netsys-lab/idint-traceroute) for the AS containers.
#
# CGO_ENABLED=1 native linux-amd64 build, NOT for cross-compilation: the
# fork's pkg/fcrypto (ID-INT crypto) is cgo-only ("build constraints exclude
# all Go files" under CGO_ENABLED=0) — same limitation as build-endhost.sh.
# Portability: the build host's glibc must be <= the fleet's
# (Ubuntu 24.04, glibc 2.39); a symbol-version guard below enforces this.
#
# The tool's go.mod pins the lschulz/scion fork; this script re-pins it to
# e356d834b (the deployed ietf-126 BR/CS build) after checkout, so ID-INT wire
# + crypto stay in lockstep with the testbed BRs. Override with IDINT_TR_FORK.
#
# USAGE:
#   ./tools/build-idint-traceroute.sh   # -> .build/idint-traceroute/bin/idint-traceroute
# Env vars:
#   IDINT_TR_REPO    git URL (default https://github.com/netsys-lab/idint-traceroute)
#   IDINT_TR_COMMIT  commit to build (default pinned below)
#   IDINT_TR_WORK    clone cache dir (default ~/.cache/idint-traceroute)
set -euo pipefail

IDINT_TR_REPO="${IDINT_TR_REPO:-https://github.com/netsys-lab/idint-traceroute}"
IDINT_TR_COMMIT="${IDINT_TR_COMMIT:-bdadf1f0343d6926021ce213741eeb2d76a49fe2}"
WORK="${IDINT_TR_WORK:-$HOME/.cache/idint-traceroute}"
OUT="$(cd "$(dirname "$0")/.." && pwd)/.build/idint-traceroute/bin"

command -v go >/dev/null || { echo "error: go not found" >&2; exit 1; }

if [ ! -d "$WORK/.git" ]; then
    git clone "$IDINT_TR_REPO" "$WORK"
fi
git -C "$WORK" fetch origin --quiet
git -C "$WORK" checkout --detach --quiet --force "$IDINT_TR_COMMIT"

# Re-pin the fork to the deployed stack commit so ID-INT stays in lockstep with
# the redeployed BRs (mirrors idint-probed's go.mod pin). --force checkout above
# discards any prior run's go.mod edit before we re-apply it cleanly.
IDINT_TR_FORK="${IDINT_TR_FORK:-e356d834b}"
( cd "$WORK" \
    && go mod edit -replace "github.com/scionproto/scion=github.com/lschulz/scion@$IDINT_TR_FORK" \
    && GOFLAGS=-mod=mod go mod tidy )

mkdir -p "$OUT"
echo "Building idint-traceroute @ $IDINT_TR_COMMIT -> $OUT"
(cd "$WORK" && CGO_ENABLED=1 go build -o "$OUT/idint-traceroute" .)

# Guard: binary must not require glibc newer than the fleet
# (Ubuntu 24.04 => GLIBC_2.39 ceiling).
max_glibc="$(objdump -T "$OUT/idint-traceroute" | grep -oE 'GLIBC_[0-9]+\.[0-9]+' | sort -uV | tail -1)"
if [ "$(printf '%s\nGLIBC_2.39\n' "$max_glibc" | sort -V | tail -1)" != "GLIBC_2.39" ]; then
    echo "error: binary needs $max_glibc > GLIBC_2.39 ceiling (build on an older-glibc host)" >&2
    exit 1
fi

# Smoke test: -h must print a usage that mentions the flags we deploy with.
# (Go's flag package exits non-zero on -h, so tolerate the exit code.)
help_out="$("$OUT/idint-traceroute" -h 2>&1 || true)"
if ! grep -q -- 'sciond' <<<"$help_out" || ! grep -q -- 'local' <<<"$help_out"; then
    echo "error: smoke test failed — unexpected -h output:" >&2
    echo "$help_out" >&2
    exit 1
fi

echo "Built:"
ls -la "$OUT"
