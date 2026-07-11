#!/bin/bash
# Keep the `web`/`web2` A/AAAA records in config/coredns/scion.zone (deployed
# to CT216, svc-152) in sync with the CURRENT venue address of the two
# dual-homed name-facing containers: svc-150 (CT215, "web") and svc-153
# (CT217, "web2"). Their eth1 leg is DHCP/SLAAC on the venue network, so the
# address changes on rebuild/lease-renewal/venue-move; this re-derives it and
# rewrites only the managed A/AAAA lines, bumping the zone's SOA serial. The
# CoreDNS `file` plugin polls the zone file (default 1m) and reloads it on
# change — no CoreDNS restart needed.
#
# IPv6: only a truly global-unicast address (2000::/3) is ever published as
# AAAA — never a ULA (fd00::/8, fc00::/7) or link-local (fe80::/10) address,
# and never a temporary/privacy address (a public DNS record must be stable
# and actually reachable from the venue). If svc-150/153 has no global v6 on
# eth1, NO AAAA line is emitted for that name (and a stale one is removed).
# This mirrors the address-selection logic in tools/update-wg-endpoint.sh,
# except that script accepts a ULA fallback (fine for a private wg endpoint)
# where this one does not (a published DNS AAAA must be globally reachable).
#
# Run this ON the Proxmox host (root, has `pct`). Safe to run before CoreDNS
# is deployed to CT216 (soft-skips) and safe to run while svc-150/153 are
# still booting (soft-skips under AUTO=1, mirroring wg-endpoint-sync's
# contract: a periodic/boot-time run never fails the unit; a manual run
# errors hard so a human notices).
#
# USAGE (on ietf-proxmox):        bash proxmox/coredns-zone-sync.sh
# USAGE (timer/boot, soft-skip):  AUTO=1 bash proxmox/coredns-zone-sync.sh
#
# TEST MODE: set ZONE_SYNC_TEST_DIR to a scratch directory and every `pct`
# call is replaced by reads/writes of local files under it, so this script
# can be exercised without a real Proxmox host / real containers:
#   $ZONE_SYNC_TEST_DIR/zone                 current CT216 zone file (or
#                                             absent, to simulate "not
#                                             deployed yet")
#   $ZONE_SYNC_TEST_DIR/status/<ct>          "running" or "stopped"
#                                             (absent == running)
#   $ZONE_SYNC_TEST_DIR/ip/<ct>-v4.txt       mock `ip -o -4 addr show dev eth1
#                                             scope global` text output
#                                             (absent/empty == no v4)
#   $ZONE_SYNC_TEST_DIR/ip/<ct>-v6.txt       mock `ip -o -6 addr show dev eth1
#                                             scope global` text output
#                                             (absent/empty == no v6)
# See proxmox/coredns-zone-sync-test.sh for the self-test harness that drives
# this.
set -euo pipefail

# name -> source CT (eth1 venue address of this CT feeds the name's A/AAAA)
declare -A MAP=( [web]=215 [web2]=217 )
NAMES=(web web2)
DNS_CT="${DNS_CT:-216}"
ZONE_PATH="${ZONE_PATH:-/etc/coredns/scion.zone}"

# populated by main(), read by rewrite_zone()
declare -A NEWV4=()
declare -A NEWV6=()
declare -A SKIP=()

log() { echo "$*"; }
die() { echo "ERROR: $*" >&2; exit 1; }

# --- pct wrappers (real vs. ZONE_SYNC_TEST_DIR test mode) ------------------

ct_status_running() {
    local ct="$1"
    if [ -n "${ZONE_SYNC_TEST_DIR:-}" ]; then
        local f="$ZONE_SYNC_TEST_DIR/status/$ct"
        if [ -f "$f" ]; then
            [ "$(cat "$f")" = "running" ]
        else
            return 0
        fi
    else
        pct status "$ct" 2>/dev/null | grep -q '^status: running'
    fi
}

ct_ip_text_v4() {
    local ct="$1"
    if [ -n "${ZONE_SYNC_TEST_DIR:-}" ]; then
        cat "$ZONE_SYNC_TEST_DIR/ip/${ct}-v4.txt" 2>/dev/null || true
    else
        pct exec "$ct" -- ip -o -4 addr show dev eth1 scope global 2>/dev/null || true
    fi
}

ct_ip_text_v6() {
    local ct="$1"
    if [ -n "${ZONE_SYNC_TEST_DIR:-}" ]; then
        cat "$ZONE_SYNC_TEST_DIR/ip/${ct}-v6.txt" 2>/dev/null || true
    else
        pct exec "$ct" -- ip -o -6 addr show dev eth1 scope global 2>/dev/null || true
    fi
}

zone_fetch() {
    if [ -n "${ZONE_SYNC_TEST_DIR:-}" ]; then
        local f="$ZONE_SYNC_TEST_DIR/zone"
        [ -f "$f" ] && cat "$f"
        return 0
    else
        pct exec "$DNS_CT" -- cat "$ZONE_PATH" 2>/dev/null || true
    fi
}

zone_push() {
    # $content has had its trailing newline(s) stripped by the `$()` calls
    # upstream (rewrite_zone/bump_serial); restore exactly one, matching the
    # zone file's original terminal newline.
    local content="$1"
    if [ -n "${ZONE_SYNC_TEST_DIR:-}" ]; then
        printf '%s\n' "$content" >"$ZONE_SYNC_TEST_DIR/zone"
    else
        local tmp
        tmp="$(mktemp)"
        printf '%s\n' "$content" >"$tmp"
        pct push "$DNS_CT" "$tmp" "$ZONE_PATH" --user root --group root --perms 0644
        rm -f "$tmp"
    fi
}

# --- address selection (mirrors tools/update-wg-endpoint.sh's `ip -o` + awk
# parsing — no `jq` dependency; ietf-proxmox does not have jq installed) ----

# First global IPv4 on eth1.
select_v4() {
    local ct="$1"
    ct_ip_text_v4 "$ct" | awk '{print $4}' | cut -d/ -f1 | head -1
}

# Global-unicast (2000::/3) only; never ULA (fd00::/8, fc00::/7) or
# link-local (fe80::/10); never a temporary/privacy or deprecated address (a
# published DNS AAAA must be stable and actually reachable from the venue —
# stricter than the ULA-tolerant wg endpoint script).
select_v6() {
    local ct="$1" a
    while IFS= read -r a; do
        [ -z "$a" ] && continue
        case "$a" in
        fd* | fc* | fe80*) continue ;;
        2* | 3*)
            printf '%s' "$a"
            return 0
            ;;
        esac
    done < <(ct_ip_text_v6 "$ct" | grep -vE 'temporary|deprecated' | awk '{print $4}' | cut -d/ -f1)
    printf ''
}

# --- zone editing ------------------------------------------------------

# Rewrite only the managed web/web2 A/AAAA lines (per NEWV4/NEWV6/SKIP);
# every other line (SOA, NS, SVCB, TXT, CNAME, comments...) passes through
# byte-identical.
rewrite_zone() {
    local zone="$1"
    local out="" line name
    local -a w
    while IFS= read -r line || [ -n "$line" ]; do
        read -r -a w <<<"$line"
        name="${w[0]:-}"
        if [[ -n "${MAP[$name]+x}" ]] && [ "${SKIP[$name]:-0}" != 1 ]; then
            if [ "${w[2]:-}" = "AAAA" ]; then
                # drop; re-emitted right after the A line below if still current
                continue
            fi
            if [ "${w[2]:-}" = "A" ]; then
                out+="${line/${w[3]}/${NEWV4[$name]}}"$'\n'
                if [ -n "${NEWV6[$name]:-}" ]; then
                    out+="$(printf '%-11sIN AAAA %s' "$name" "${NEWV6[$name]}")"$'\n'
                fi
                continue
            fi
        fi
        out+="$line"$'\n'
    done <<<"$zone"
    printf '%s' "$out"
}

# Returns 1 (no output) if no SOA line is found, instead of dying directly:
# extract_serial runs inside a `$(...)` command substitution in main(), which
# is a subshell — an `exit` in here would only kill that subshell, not the
# script, and a plain `die` would hard-fail even under AUTO=1. Let the caller
# decide (mirrors the AUTO-gated checks earlier in main()).
extract_serial() {
    local zone="$1" line
    local -a w
    while IFS= read -r line || [ -n "$line" ]; do
        read -r -a w <<<"$line"
        if [ "${w[0]:-}" = "@" ] && [ "${w[2]:-}" = "IN" ] && [ "${w[3]:-}" = "SOA" ]; then
            printf '%s' "${w[6]:-}"
            return 0
        fi
    done <<<"$zone"
    return 1
}

# date-based YYYYMMDDnn: same-day bump increments nn (2-digit, capped at 99
# with a warning); otherwise today's date with nn=01.
next_serial() {
    local old="$1" today old_date old_nn new_nn
    today="$(date +%Y%m%d)"
    if [ "${#old}" -ge 10 ]; then
        old_date="${old:0:8}"
        old_nn="${old:8:2}"
    else
        old_date=""
        old_nn="00"
    fi
    if [ "$old_date" = "$today" ]; then
        new_nn=$((10#$old_nn + 1))
        if [ "$new_nn" -gt 99 ]; then
            echo "WARNING: SOA serial nn wrapped at 99 for $today" >&2
            new_nn=99
        fi
        printf '%s%02d' "$today" "$new_nn"
    else
        printf '%s01' "$today"
    fi
}

bump_serial() {
    local zone="$1" old="$2" new="$3"
    local out="" line
    local -a w
    while IFS= read -r line || [ -n "$line" ]; do
        read -r -a w <<<"$line"
        if [ "${w[0]:-}" = "@" ] && [ "${w[2]:-}" = "IN" ] && [ "${w[3]:-}" = "SOA" ]; then
            out+="${line/$old/$new}"$'\n'
        else
            out+="$line"$'\n'
        fi
    done <<<"$zone"
    printf '%s' "$out"
}

# --- main --------------------------------------------------------------

main() {
    local zone_content
    zone_content="$(zone_fetch)"
    if [ -z "$zone_content" ]; then
        log "$ZONE_PATH not found on CT$DNS_CT (CoreDNS not deployed yet?) - skipping"
        exit 0
    fi

    local name ct v4 v6
    for name in "${NAMES[@]}"; do
        ct="${MAP[$name]}"
        if ! ct_status_running "$ct"; then
            if [ "${AUTO:-0}" = "1" ]; then
                log "CT$ct ($name) is not running - soft-skip"
                SKIP[$name]=1
                continue
            else
                die "CT$ct ($name) is not running"
            fi
        fi

        v4="$(select_v4 "$ct")"
        if [ -z "$v4" ]; then
            if [ "${AUTO:-0}" = "1" ]; then
                log "CT$ct ($name) has no global IPv4 on eth1 yet - soft-skip"
                SKIP[$name]=1
                continue
            else
                die "CT$ct ($name) has no global IPv4 on eth1"
            fi
        fi

        v6="$(select_v6 "$ct")"
        NEWV4[$name]="$v4"
        NEWV6[$name]="$v6"
        log "CT$ct ($name) eth1 -> v4=$v4 v6=${v6:-<none>}"
    done

    local candidate
    candidate="$(rewrite_zone "$zone_content")"
    if [ "$candidate" = "$zone_content" ]; then
        log "zone unchanged"
        exit 0
    fi

    local old_serial new_serial final
    if ! old_serial="$(extract_serial "$zone_content")"; then
        if [ "${AUTO:-0}" = "1" ]; then
            log "WARNING: no SOA record found in zone - skipping (soft-skip)"
            exit 0
        else
            die "no SOA record found in zone"
        fi
    fi
    new_serial="$(next_serial "$old_serial")"
    final="$(bump_serial "$candidate" "$old_serial" "$new_serial")"

    zone_push "$final"
    log "zone updated (serial $old_serial -> $new_serial), pushed to CT$DNS_CT:$ZONE_PATH"
}

main "$@"
