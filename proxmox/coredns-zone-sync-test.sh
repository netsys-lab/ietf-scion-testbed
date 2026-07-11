#!/bin/bash
# Self-test harness for coredns-zone-sync.sh. Exercises it entirely through
# ZONE_SYNC_TEST_DIR (see the header comment in that script) — no `pct`, no
# real containers, no real Proxmox host required. Run from anywhere:
#
#   bash proxmox/coredns-zone-sync-test.sh
#
# Exits 0 iff every case passes; prints PASS/FAIL per case plus a summary.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SYNC_SH="$SCRIPT_DIR/coredns-zone-sync.sh"
SEED_ZONE="$SCRIPT_DIR/../config/coredns/scion.zone"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

PASS=0
FAIL=0

pass() {
    PASS=$((PASS + 1))
    echo "PASS: $1"
}

fail() {
    FAIL=$((FAIL + 1))
    echo "FAIL: $1"
}

assert_contains() {
    local desc="$1" haystack="$2" needle="$3"
    if [[ "$haystack" == *"$needle"* ]]; then
        pass "$desc"
    else
        fail "$desc (expected to find: $needle)"
    fi
}

assert_not_contains() {
    local desc="$1" haystack="$2" needle="$3"
    if [[ "$haystack" != *"$needle"* ]]; then
        pass "$desc"
    else
        fail "$desc (did not expect to find: $needle)"
    fi
}

assert_eq() {
    local desc="$1" got="$2" want="$3"
    if [ "$got" = "$want" ]; then
        pass "$desc"
    else
        fail "$desc (got '$got', want '$want')"
    fi
}

# Byte-level check (command substitution would strip a trailing newline, so
# this reads raw bytes off disk instead of comparing shell strings): the
# pushed zone file must end with exactly one newline, not zero (stripped by
# `$()`) and not two (double-appended).
assert_single_trailing_newline() {
    local desc="$1" file="$2" size lastbyte secondlast
    size="$(wc -c <"$file")"
    if [ "$size" -eq 0 ]; then
        fail "$desc (empty file)"
        return
    fi
    lastbyte="$(tail -c1 "$file" | od -An -tx1 | tr -d ' \n')"
    if [ "$lastbyte" != "0a" ]; then
        fail "$desc (last byte is 0x$lastbyte, not a newline)"
        return
    fi
    if [ "$size" -ge 2 ]; then
        secondlast="$(tail -c2 "$file" | head -c1 | od -An -tx1 | tr -d ' \n')"
        if [ "$secondlast" = "0a" ]; then
            fail "$desc (double trailing newline)"
            return
        fi
    fi
    pass "$desc"
}

# runs the script in test mode against $1 (the ZONE_SYNC_TEST_DIR), with
# AUTO defaulting to 1 (timer/boot-style soft-skip semantics); prints combined
# stdout+stderr to stdout and returns its exit code via $RC.
run_sync() {
    local dir="$1" auto="${2:-1}"
    OUT="$(ZONE_SYNC_TEST_DIR="$dir" AUTO="$auto" bash "$SYNC_SH" 2>&1)"
    RC=$?
}

serial_of() {
    grep -E ' IN SOA ' "$1" | awk '{print $7}'
}

# Mimic `ip -o -4/-6 addr show dev eth1 scope global` single-line text output
# (no jq/JSON — the production Proxmox host has no jq installed; parsing
# mirrors tools/update-wg-endpoint.sh's `ip -o` + awk style).
mk_v4() {
    printf '2: eth1    inet %s/24 brd 192.0.2.255 scope global eth1\\       valid_lft forever preferred_lft forever\n' "$1"
}

mk_v6() {
    printf '3: eth1    inet6 %s/64 scope global dynamic mngtmpaddr noprefixroute \\       valid_lft 86392sec preferred_lft 14392sec\n' "$1"
}

mk_v6_temporary() {
    printf '3: eth1    inet6 %s/64 scope global temporary dynamic \\       valid_lft 86392sec preferred_lft 14392sec\n' "$1"
}

echo "== setup: main scenario dir =="
MAIN="$WORK/main"
mkdir -p "$MAIN/status" "$MAIN/ip"
cp "$SEED_ZONE" "$MAIN/zone"
mk_v4 "192.0.2.215" >"$MAIN/ip/215-v4.txt"
mk_v4 "192.0.2.217" >"$MAIN/ip/217-v4.txt"
mk_v6 "2001:db8::215" >"$MAIN/ip/215-v6.txt"
seed_serial="$(serial_of "$MAIN/zone")"

echo
echo "== case 1: initial placeholder rewrite =="
run_sync "$MAIN"
assert_eq "case1: exits 0" "$RC" 0
zone1="$(cat "$MAIN/zone")"
assert_contains "case1: web A rewritten to real v4" "$zone1" "web        IN A    192.0.2.215"
assert_contains "case1: web AAAA added (global v6 present)" "$zone1" "web        IN AAAA 2001:db8::215"
assert_contains "case1: web2 A rewritten to real v4" "$zone1" "web2       IN A    192.0.2.217"
assert_not_contains "case1: web2 has no AAAA (no global v6 fixture)" "$zone1" "web2       IN AAAA"
assert_not_contains "case1: placeholder 0.0.0.0 gone" "$zone1" "0.0.0.0"
assert_contains "case1: SVCB lines pass through untouched" "$zone1" 'web        IN SVCB 1 . alpn=h3,h2 port=443 scion=1-150\,10.20.3.215'
assert_contains "case1: unrelated records pass through untouched" "$zone1" 'games      IN TXT  "scion=71-2:0:4a,10.44.25.3"'
assert_single_trailing_newline "case1: pushed zone ends with a single trailing newline" "$MAIN/zone"
serial1="$(serial_of "$MAIN/zone")"
if [ "$serial1" != "$seed_serial" ]; then
    pass "case1: SOA serial bumped from seed ($seed_serial -> $serial1)"
else
    fail "case1: SOA serial should have bumped from seed ($seed_serial)"
fi

echo
echo "== case 2: no-change idempotent run =="
run_sync "$MAIN"
assert_eq "case2: exits 0" "$RC" 0
assert_contains "case2: prints 'zone unchanged'" "$OUT" "zone unchanged"
zone2="$(cat "$MAIN/zone")"
assert_eq "case2: zone file byte-identical to after case1" "$zone2" "$zone1"
assert_eq "case2: serial not bumped again" "$(serial_of "$MAIN/zone")" "$serial1"

echo
echo "== case 3: v4 change =="
mk_v4 "192.0.2.199" >"$MAIN/ip/215-v4.txt"
run_sync "$MAIN"
assert_eq "case3: exits 0" "$RC" 0
zone3="$(cat "$MAIN/zone")"
assert_contains "case3: web A picks up new v4" "$zone3" "web        IN A    192.0.2.199"
assert_not_contains "case3: old v4 gone" "$zone3" "192.0.2.215   ;"
assert_single_trailing_newline "case3: re-pushed zone still ends with a single trailing newline" "$MAIN/zone"
serial3="$(serial_of "$MAIN/zone")"
assert_eq "case3 (serial same-day increment): date part unchanged" "${serial3:0:8}" "${serial1:0:8}"
if [ "${serial3:8:2}" -gt "${serial1:8:2}" ] 2>/dev/null; then
    pass "case3 (serial same-day increment): nn incremented ($serial1 -> $serial3)"
else
    fail "case3 (serial same-day increment): nn should have incremented ($serial1 -> $serial3)"
fi

echo
echo "== case 4: v6 appearing =="
mk_v6 "2001:db8::217" >"$MAIN/ip/217-v6.txt"
run_sync "$MAIN"
assert_eq "case4: exits 0" "$RC" 0
zone4="$(cat "$MAIN/zone")"
assert_contains "case4: web2 AAAA now present" "$zone4" "web2       IN AAAA 2001:db8::217"
serial4="$(serial_of "$MAIN/zone")"
assert_eq "case4: serial date part still today" "${serial4:0:8}" "${serial1:0:8}"
if [ "${serial4:8:2}" -gt "${serial3:8:2}" ] 2>/dev/null; then
    pass "case4: nn incremented again ($serial3 -> $serial4)"
else
    fail "case4: nn should have incremented again ($serial3 -> $serial4)"
fi

echo
echo "== case 5: container-not-running skip =="
echo stopped >"$MAIN/status/217"
mk_v4 "192.0.2.250" >"$MAIN/ip/217-v4.txt" # would change web2 if NOT skipped
run_sync "$MAIN"
assert_eq "case5: exits 0 (soft-skip, not an error)" "$RC" 0
assert_contains "case5: logs the skip" "$OUT" "CT217 (web2) is not running - soft-skip"
zone5="$(cat "$MAIN/zone")"
assert_contains "case5: web2 A left untouched (old value kept)" "$zone5" "web2       IN A    192.0.2.217"
assert_not_contains "case5: web2 did NOT pick up the new fixture" "$zone5" "192.0.2.250"
assert_eq "case5: web (unaffected CT) still reflects prior state" "$(grep -c 'web        IN A    192.0.2.199' <<<"$zone5")" 1
assert_contains "case5: reported as unchanged (only the skipped name would've differed)" "$OUT" "zone unchanged"
echo running >"$MAIN/status/217" # restore for any future runs

echo
echo "== case 5b (bonus): manual run (no AUTO) hard-fails on stopped container =="
echo stopped >"$MAIN/status/217"
run_sync "$MAIN" 0
assert_eq "case5b: exits 1 (manual runs don't soft-skip)" "$RC" 1
assert_contains "case5b: error message names the CT" "$OUT" "CT217 (web2) is not running"
echo running >"$MAIN/status/217"

echo
echo "== case 6: zone missing (CoreDNS not deployed yet) =="
NOZONE="$WORK/nozone"
mkdir -p "$NOZONE/status" "$NOZONE/ip"
mk_v4 "192.0.2.215" >"$NOZONE/ip/215-v4.txt"
mk_v4 "192.0.2.217" >"$NOZONE/ip/217-v4.txt"
run_sync "$NOZONE"
assert_eq "case6: exits 0 (soft-skip, not an error)" "$RC" 0
assert_contains "case6: logs the missing zone" "$OUT" "not found on CT216"
assert_eq "case6: no zone file was created" "$([ -f "$NOZONE/zone" ] && echo yes || echo no)" "no"

echo
echo "== case 7: unparseable SOA (soft-skip under AUTO=1, hard-fail without) =="
BADSOA="$WORK/badsoa"
mkdir -p "$BADSOA/status" "$BADSOA/ip"
cat >"$BADSOA/zone" <<'EOF'
$ORIGIN scion.
@ IN NS scitra.netsys.ovgu.de.
web        IN A    0.0.0.0   ; placeholder, rewritten by coredns-zone-sync (venue IP of svc-150)
web        IN SVCB 1 . alpn=h3,h2 port=443 scion=1-150\,10.20.3.215
web2       IN A    0.0.0.0   ; venue IP of svc-153
web2       IN SVCB 1 . alpn=h3,h2 port=443 scion=1-153\,10.20.3.217
EOF
badsoa_before="$(cat "$BADSOA/zone")"
mk_v4 "192.0.2.215" >"$BADSOA/ip/215-v4.txt"
mk_v4 "192.0.2.217" >"$BADSOA/ip/217-v4.txt"
run_sync "$BADSOA"
assert_eq "case7: AUTO=1 soft-skips (exits 0) on unparseable SOA" "$RC" 0
assert_contains "case7: AUTO=1 logs a warning naming the missing SOA" "$OUT" "no SOA record found in zone"
assert_eq "case7: AUTO=1 leaves the zone file untouched (no serial to bump)" "$(cat "$BADSOA/zone")" "$badsoa_before"
run_sync "$BADSOA" 0
assert_eq "case7b: manual run (no AUTO) hard-fails on unparseable SOA" "$RC" 1
assert_contains "case7b: error message names the missing SOA" "$OUT" "no SOA record found in zone"

echo
echo "== case 8: v6 selection edge cases against the ip -o (no-jq) parser =="
V6EDGE="$WORK/v6edge"
mkdir -p "$V6EDGE/status" "$V6EDGE/ip"
cp "$SEED_ZONE" "$V6EDGE/zone"
mk_v4 "192.0.2.215" >"$V6EDGE/ip/215-v4.txt"
mk_v4 "192.0.2.217" >"$V6EDGE/ip/217-v4.txt"
mk_v6_temporary "2001:db8::dead" >"$V6EDGE/ip/215-v6.txt"
run_sync "$V6EDGE"
assert_eq "case8a: exits 0" "$RC" 0
zone8a="$(cat "$V6EDGE/zone")"
assert_not_contains "case8a: temporary-only v6 yields no AAAA for web" "$zone8a" "web        IN AAAA"

cat >"$V6EDGE/ip/215-v6.txt" <<'EOF'
3: eth1    inet6 fd00:1234::215/64 scope global dynamic \       valid_lft forever preferred_lft forever
3: eth1    inet6 2001:db8::cafe/64 scope global dynamic mngtmpaddr noprefixroute \       valid_lft 86392sec preferred_lft 14392sec
EOF
run_sync "$V6EDGE"
assert_eq "case8b: exits 0" "$RC" 0
zone8b="$(cat "$V6EDGE/zone")"
assert_contains "case8b: mixed ULA+global picks the global-unicast v6" "$zone8b" "web        IN AAAA 2001:db8::cafe"
assert_not_contains "case8b: ULA never selected" "$zone8b" "fd00:1234::215"

echo
echo "=================================================="
echo "PASS: $PASS  FAIL: $FAIL"
if [ "$FAIL" -eq 0 ]; then
    echo "ALL TESTS PASSED"
    exit 0
else
    echo "TESTS FAILED"
    exit 1
fi
