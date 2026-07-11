#!/usr/bin/env bash
# devbox_test.sh — end-to-end integration check for the hev3 + forked-CoreDNS
# stack (SCION SVCB records + Happy Eyeballs v3 client), entirely WITHOUT
# testbed access: full pipeline SVCB parse -> scitra-absent drop -> IP race
# -> fetch, against a local CoreDNS instance serving the real
# config/coredns/{Corefile,scion.zone} (rewritten to loopback) and a local
# hev3-server carrying the committed web.scion demo cert.
#
# See .superpowers/sdd/task-13-brief.md, Task 13 step 1.
#
# Usage: bash hev3/integration/devbox_test.sh   (run from anywhere)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
HEV3_DIR="$REPO_ROOT/hev3"
CONFIG_DIR="$REPO_ROOT/config/coredns"
CA_DIR="$REPO_ROOT/ansible/files/hev3-ca"

DNS_PORT=15353
SERVER_PORT=14443

STEP="init"
step() { STEP="$1"; echo "== $1 =="; }
fail() { echo "FAIL at step: $STEP: $*" >&2; exit 1; }

# --- cleanup: always kill children + scratch dir, even on failure/Ctrl-C ---
PIDS=()
SCRATCH=""
cleanup() {
    local pid
    for pid in "${PIDS[@]:-}"; do
        [ -z "$pid" ] && continue
        if kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
    done
    if [ -n "$SCRATCH" ] && [ -d "$SCRATCH" ]; then
        rm -rf "$SCRATCH"
    fi
}
trap cleanup EXIT INT TERM

# wait_ready polls check_fn (a shell function taking no args, returning 0 when
# ready) while also confirming pid is still alive, up to ~15s.
wait_ready() {
    local desc="$1" pid="$2" check_fn="$3"
    local i=0
    while true; do
        if ! kill -0 "$pid" 2>/dev/null; then
            fail "$desc exited before becoming ready (see $SCRATCH/$desc.log)"
        fi
        if "$check_fn"; then
            return 0
        fi
        i=$((i + 1))
        if [ "$i" -ge 150 ]; then
            fail "$desc did not become ready within timeout"
        fi
        sleep 0.1
    done
}

dns_ready() { dig @127.0.0.1 -p "$DNS_PORT" SVCB web.scion +short 2>/dev/null | grep -q .; }
server_ready() { (exec 3<>"/dev/tcp/127.0.0.1/$SERVER_PORT") 2>/dev/null; }

step "preflight: ports free"
for p in "$DNS_PORT" "$SERVER_PORT"; do
    if ss -ltnu 2>/dev/null | awk '{print $5}' | grep -qE "[:.]${p}\$"; then
        fail "port $p is already in use on this host"
    fi
done

step "build coredns"
COREDNS_BIN="$REPO_ROOT/.build/coredns/bin/coredns"
if [ ! -x "$COREDNS_BIN" ]; then
    "$REPO_ROOT/tools/build-coredns.sh" || fail "tools/build-coredns.sh failed"
fi
[ -x "$COREDNS_BIN" ] || fail "coredns binary missing at $COREDNS_BIN"
echo "coredns: $COREDNS_BIN ($("$COREDNS_BIN" -version))"

step "build hev3 + hev3-server"
(cd "$HEV3_DIR" && make build) || fail "hev3 'make build' failed"
HEV3_BIN="$HEV3_DIR/bin/hev3"
HEV3_SERVER_BIN="$HEV3_DIR/bin/hev3-server"
[ -x "$HEV3_BIN" ] || fail "hev3 binary missing at $HEV3_BIN"
[ -x "$HEV3_SERVER_BIN" ] || fail "hev3-server binary missing at $HEV3_SERVER_BIN"

step "stage runtime dir"
SCRATCH="$(mktemp -d)"
cp "$CONFIG_DIR/Corefile" "$SCRATCH/Corefile"
cp "$CONFIG_DIR/scion.zone" "$SCRATCH/scion.zone"

# Local ports (scion. and . server blocks both move off 53) + root pointed at
# the scratch dir so the `file` plugin loads our staged scion.zone copy.
sed -i \
    -e "s|^scion\. {|scion.:${DNS_PORT} {|" \
    -e "s|^\. {|.:${DNS_PORT} {|" \
    -e "s|root /etc/coredns|root ${SCRATCH}|" \
    -e "s|bind 10.20.3.216|bind 127.0.0.1|" \
    "$SCRATCH/Corefile"
grep -q "^scion\.:${DNS_PORT} {" "$SCRATCH/Corefile" || fail "Corefile scion. server block rewrite did not apply"
grep -q "^\.:${DNS_PORT} {" "$SCRATCH/Corefile" || fail "Corefile . server block rewrite did not apply"

# web's placeholder A -> 127.0.0.1 (local hev3-server host), and its SVCB
# port=443 -> the local hev3-server's actual listen port. Anchored on
# "web" + whitespace so web2's lines are untouched (sed pattern "web" alone
# would also match "web2").
sed -i -E \
    -e "/^web[[:space:]]/ s/0\.0\.0\.0/127.0.0.1/" \
    -e "/^web[[:space:]]/ s/port=443/port=${SERVER_PORT}/" \
    "$SCRATCH/scion.zone"
grep -qE '^web[[:space:]]+IN A[[:space:]]+127\.0\.0\.1' "$SCRATCH/scion.zone" || fail "scion.zone web A rewrite did not apply"
grep -qE "^web[[:space:]].*port=${SERVER_PORT}" "$SCRATCH/scion.zone" || fail "scion.zone web SVCB port rewrite did not apply"
grep -qE '^web2[[:space:]]+IN A[[:space:]]+0\.0\.0\.0' "$SCRATCH/scion.zone" || fail "scion.zone web2 A line unexpectedly touched"

step "start coredns"
"$COREDNS_BIN" -conf "$SCRATCH/Corefile" >"$SCRATCH/coredns.log" 2>&1 &
COREDNS_PID=$!
PIDS+=("$COREDNS_PID")
wait_ready "coredns" "$COREDNS_PID" dns_ready

step "start hev3-server"
"$HEV3_SERVER_BIN" \
    -listen-ip "127.0.0.1:${SERVER_PORT}" \
    -cert "$CA_DIR/web.scion/cert.pem" \
    -key "$CA_DIR/web.scion/key.pem" \
    >"$SCRATCH/hev3-server.log" 2>&1 &
SERVER_PID=$!
PIDS+=("$SERVER_PID")
wait_ready "hev3-server" "$SERVER_PID" server_ready

step "dig SVCB web.scion"
SVCB_OUT="$(dig @127.0.0.1 -p "$DNS_PORT" SVCB web.scion +short)"
[ -n "$SVCB_OUT" ] || fail "SVCB web.scion returned empty"
echo "SVCB web.scion -> $SVCB_OUT"

step "dig AAAA games.scion (scitra synth)"
AAAA_OUT="$(dig @127.0.0.1 -p "$DNS_PORT" AAAA games.scion +short)"
# fc00::/8 fixes only the first byte ("fc"); the ISD/ASN encoding fills the
# rest, so e.g. "fc04:7800:4a00::..." is a valid in-prefix synth.
echo "$AAAA_OUT" | grep -qi '^fc[0-9a-f][0-9a-f]:' || fail "AAAA games.scion did not return an fc00::/8 scitra synth: '$AAAA_OUT'"
echo "AAAA games.scion -> $AAAA_OUT"

step "dig A web.scion"
A_OUT="$(dig @127.0.0.1 -p "$DNS_PORT" A web.scion +short)"
[ "$A_OUT" = "127.0.0.1" ] || fail "A web.scion = '$A_OUT', want 127.0.0.1"
echo "A web.scion -> $A_OUT"

step "hev3 fetch https://web.scion/ (--json)"
JSON_OUT="$SCRATCH/hev3.json"
if ! "$HEV3_BIN" \
    --resolver "127.0.0.1:${DNS_PORT}" --ca "$CA_DIR/ca.pem" --json --timeout 10s "https://web.scion/" \
    >"$JSON_OUT" 2>"$SCRATCH/hev3.err"; then
    cat "$SCRATCH/hev3.err" >&2
    fail "hev3 fetch of https://web.scion/ exited non-zero"
fi

step "assert hev3 --json result"
if ! python3 - "$JSON_OUT" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path) as f:
    d = json.load(f)

winner = d.get("winner", {})
if not winner.get("label"):
    sys.exit(f"empty winner.label: {d}")

status = d.get("status", "")
if "200" not in status:
    sys.exit(f"status missing 200: {status!r}")

timeline = d.get("timeline", [])
if not timeline:
    sys.exit("empty timeline")

kinds = {e.get("kind") for e in timeline}
missing = {"query", "candidate", "attempt"} - kinds
if missing:
    sys.exit(f"timeline missing event kinds {missing}: seen {sorted(kinds)}")

# devbox has no sciond and no fc00 scitra route (verified by this script's
# preflight assumptions) -> ExpandSCION must drop the SCION candidate before
# racing: no "attempt" ever fires on a scion: label, and a "fail" note
# records the drop.
scion_attempts = [e for e in timeline if e.get("kind") == "attempt" and e.get("label", "").startswith("scion:")]
if scion_attempts:
    sys.exit(f"unexpected SCION attempt on a devbox with no daemon/scitra route: {scion_attempts}")

scion_drop_noted = any(
    e.get("kind") == "fail" and e.get("label", "").startswith("scion:")
    for e in timeline
)
if not scion_drop_noted:
    sys.exit(f"expected a SCION-candidate drop ('fail') note in the timeline: {timeline}")

print(f"winner={winner['label']} family={winner.get('family')} status={status!r} events={len(timeline)}")
PY
then
    fail "hev3 --json assertions failed"
fi

step "hev3 fetch https://web.scion/whoami (human output)"
WHOAMI_OUT="$SCRATCH/hev3-whoami.txt"
if ! "$HEV3_BIN" \
    --resolver "127.0.0.1:${DNS_PORT}" --ca "$CA_DIR/ca.pem" --timeout 10s "https://web.scion/whoami" \
    >"$WHOAMI_OUT" 2>"$SCRATCH/hev3-whoami.err"; then
    cat "$SCRATCH/hev3-whoami.err" >&2
    fail "hev3 fetch of https://web.scion/whoami exited non-zero"
fi
grep -q "WINNER" "$WHOAMI_OUT" || fail "whoami run: human output missing WINNER marker"
grep -Eq '"transport":"ip-h[23]"' "$WHOAMI_OUT" || fail "whoami body missing ip-h2/ip-h3 transport tag: $(cat "$WHOAMI_OUT")"
echo "whoami body confirms hev3-server answered over $(grep -Eo '"transport":"ip-h[23]"' "$WHOAMI_OUT")"

echo
echo "PASS"
exit 0
