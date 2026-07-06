#!/bin/bash
# Build idint-traceroute (ID-INT telemetry traceroute/debug tool,
# github.com/netsys-lab/idint-traceroute) for the AS containers.
#
# CGO_ENABLED=1 native linux-amd64 build, NOT for cross-compilation: the
# fork's pkg/fcrypto (ID-INT crypto) is cgo-only ("build constraints exclude
# all Go files" under CGO_ENABLED=0) — same limitation as build-endhost.sh.
# Portability: the build host's glibc must be <= the oldest container's
# (Debian 12, glibc 2.36); a symbol-version guard below enforces this.
#
# The tool's go.mod pins the lschulz/scion fork at 8ce7ed2f857d — the exact
# upstream base of the deployed fork (158d2060b, ietf-126) — so ID-INT wire
# compatibility with the testbed BRs is by construction.
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
git -C "$WORK" checkout --detach --quiet "$IDINT_TR_COMMIT"

mkdir -p "$OUT"
echo "Building idint-traceroute @ $IDINT_TR_COMMIT -> $OUT"
(cd "$WORK" && CGO_ENABLED=1 go build -o "$OUT/idint-traceroute" .)

# Guard: binary must not require glibc newer than the oldest container
# (Debian 12 => GLIBC_2.36 ceiling).
max_glibc="$(objdump -T "$OUT/idint-traceroute" | grep -oE 'GLIBC_[0-9]+\.[0-9]+' | sort -uV | tail -1)"
if [ "$(printf '%s\nGLIBC_2.36\n' "$max_glibc" | sort -V | tail -1)" != "GLIBC_2.36" ]; then
    echo "error: binary needs $max_glibc > GLIBC_2.36 ceiling (build on an older-glibc host)" >&2
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
