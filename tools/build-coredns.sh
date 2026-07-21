#!/bin/bash
# Build CoreDNS with SCION support from upstream + patch: clones
# github.com/coredns/coredns at a pinned commit, applies
# tools/coredns-scion.patch (the scitra plugin — SCION-IP-translator AAAA
# synthesis, originally from netsys-lab/coredns-scitra — plus its plugin
# registration), and pins the SCION-aware DNS library
# (netsys-lab/dns-scion-svcb branch `scion`: typed scion/scion-policy SVCB
# SvcParamKeys) and the lschulz/scion fork via go.mod replaces.
# Fully static (CGO_ENABLED=0), so unlike the idint-* binaries there is no
# GLIBC ceiling guard to check.
#
# The checkout in $COREDNS_SRC (default .build/coredns/src) is a throwaway
# build dir: every run hard-resets it to $COREDNS_PIN before patching.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="${COREDNS_SRC:-$ROOT/.build/coredns/src}"
OUT="$ROOT/.build/coredns/bin"
PATCH="$ROOT/tools/coredns-scion.patch"

# Pins — bump together: the patch is generated against COREDNS_PIN. The two
# module pins are pseudo-versions (v0.0.0-<commit-utc>-<sha12>): go mod tidy
# rejects raw hashes in replace directives.
COREDNS_PIN=2d42f0e8f55f4bf36b5bb29577506d1be7e462bf                       # coredns/coredns master
DNS_FORK=github.com/netsys-lab/dns-scion-svcb@v0.0.0-20260719065454-e94f8373acf8  # branch scion
SCION_FORK=github.com/lschulz/scion@v0.11.1-0.20260709203036-e356d834bba6         # branch ietf-126

command -v go >/dev/null  || { echo "error: go not found" >&2; exit 1; }
command -v git >/dev/null || { echo "error: git not found" >&2; exit 1; }
[ -f "$PATCH" ] || { echo "error: $PATCH missing" >&2; exit 1; }

if [ ! -d "$SRC/.git" ]; then
    mkdir -p "$SRC"
    git -C "$SRC" init -q
    git -C "$SRC" remote add origin https://github.com/coredns/coredns.git
fi
if ! git -C "$SRC" cat-file -e "$COREDNS_PIN" 2>/dev/null; then
    echo "Fetching coredns @ $COREDNS_PIN"
    git -C "$SRC" fetch -q --depth 1 origin "$COREDNS_PIN"
fi
git -C "$SRC" checkout -qf "$COREDNS_PIN"
git -C "$SRC" clean -qfd

echo "Applying $PATCH"
git -C "$SRC" apply "$PATCH"

(
    cd "$SRC"
    go mod edit -replace "github.com/miekg/dns=${DNS_FORK%@*}@${DNS_FORK#*@}"
    go mod edit -replace "github.com/scionproto/scion=${SCION_FORK%@*}@${SCION_FORK#*@}"
    go mod tidy
)

mkdir -p "$OUT"
echo "Building coredns -> $OUT"
(cd "$SRC" && CGO_ENABLED=0 go build -o "$OUT/coredns" .)

echo "Version:"
"$OUT/coredns" -version

echo "Built:"; ls -la "$OUT"
