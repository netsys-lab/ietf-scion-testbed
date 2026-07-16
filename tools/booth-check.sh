#!/bin/bash
# Booth-open health check: BGP fabric + SCION plane + DNS + hev3 smoke.
# Run from the dev box (mgmt reachable via ssh only): bash tools/booth-check.sh
# Every line must read OK; exits 1 on any FAIL. ~30s.
set -u

PLAY="${PLAY:-ietf@10.20.3.210}"   # play-158: has curl/dig/hev3 and mgmt reachability
# Override SSH to target a different testbed host, e.g. the ietf-minipc-rack
# replica: SSH="ssh -J ietf-minipc-rack -o UserKnownHostsFile=~/.ssh/known_hosts_minipc -o StrictHostKeyChecking=accept-new"
SSH="${SSH:-ssh}"
FAIL=0
ok()   { printf 'OK   %s\n' "$1"; }
fail() { printf 'FAIL %s\n' "$1"; FAIL=1; }

# One ssh: gather everything machine-readable from inside the mgmt net.
OUT=$($SSH -o ConnectTimeout=10 "$PLAY" '
set -u
est=0; total=0; shaped=0; bfddown=0
for x in 150 151 152 153 154 155 156 157 158 159 160 161; do
  bgp=$(curl -sm5 http://10.20.3.$x:30480/api/v1/bgp)
  links=$(curl -sm5 http://10.20.3.$x:30480/api/v1/links)
  python3 - "$bgp" "$links" <<PY
import json,sys
try:
    ss=json.loads(sys.argv[1])["sessions"]
    print("S", len(ss),
          sum(1 for s in ss if s["state"]=="Established"),
          sum(1 for s in ss if s.get("bfd")!="Up"))
except Exception: print("S 0 0 999")
try:
    print("L", sum(1 for l in json.loads(sys.argv[2]) if l["shaped"]))
except Exception: print("L 999")
PY
done
echo "HEALTH $(curl -sm5 http://10.20.3.200:8080/api/health)"
echo "DIGWEB $(dig +short +time=3 A web.scion @10.20.3.216 | head -1)"
echo "DIGANCHOR $(dig +short +time=3 A as150.scion @10.20.3.216 | head -1)"
')

total=$(awk '$1=="S"{t+=$2} END{print t+0}' <<<"$OUT")
est=$(awk '$1=="S"{e+=$3} END{print e+0}' <<<"$OUT")
bfddown=$(awk '$1=="S"{b+=$4} END{print b+0}' <<<"$OUT")
shaped=$(awk '$1=="L"{s+=$2} END{print s+0}' <<<"$OUT")

[ "$total" = 48 ] && [ "$est" = 48 ] && ok "BGP sessions 48/48 Established" \
                                     || fail "BGP sessions $est/$total Established"
[ "$bfddown" = 0 ] && ok "BFD up on all sessions" || fail "BFD down on $bfddown session(s)"
[ "$shaped" = 0 ] && ok "0 links shaped" || fail "$shaped link(s) still shaped — reset before opening"

HEALTH=$(grep '^HEALTH' <<<"$OUT" | cut -d' ' -f2-)
python3 - "$HEALTH" <<'PY' && ok "fabricd health: linkd 12/12, all targets up" || FAIL=1
import json,sys
d=json.loads(sys.argv[1])
assert sum(d["linkd"].values())==len(d["linkd"])==12, d["linkd"]
down=[k for k,v in d["targets"].items() if not v]
assert not down, down
PY
[ "$FAIL" = 1 ] && grep -q . <<<"$HEALTH" && python3 -c "
import json,sys; d=json.loads('''$HEALTH''')
print('     linkd:', sum(d['linkd'].values()), '/', len(d['linkd']),
      '| targets down:', [k for k,v in d['targets'].items() if not v])" 2>/dev/null

grep -q '^DIGWEB 10.150.0.80' <<<"$OUT" && ok "DNS web.scion -> 10.150.0.80" \
                                        || fail "DNS web.scion: $(grep '^DIGWEB' <<<"$OUT")"
grep -q '^DIGANCHOR 10.150.0.1' <<<"$OUT" && ok "DNS as150.scion -> 10.150.0.1" \
                                          || fail "DNS as150.scion: $(grep '^DIGANCHOR' <<<"$OUT")"

# hev3 smoke: fair race (IP wins on merit since the 0.2.0 engine no longer
# stalls IP legs behind SCION path lookup), the demo's grace race (SCION
# wins with -scion-grace, the booth recipe), + IP-only (v6/v4 over fabric).
R0=$($SSH "$PLAY" 'hev3 https://web.scion/ 2>/dev/null | grep "winner:"')
grep -qE 'IPv6|IPv4|SCION' <<<"$R0" && ok "hev3 fair race ($R0)" || fail "hev3 fair race: $R0"
R1=$($SSH "$PLAY" 'hev3 -scion-grace 150ms https://web.scion/ 2>/dev/null | grep "winner:"')
grep -q 'SCION' <<<"$R1" && ok "hev3 grace race: SCION wins ($R1)" || fail "hev3 grace race: $R1"
R2=$($SSH "$PLAY" 'hev3 --no-scion https://web.scion/ 2>/dev/null | grep "winner:"')
grep -qE 'IPv6|IPv4' <<<"$R2" && ok "hev3 IP-only over fabric ($R2)" || fail "hev3 IP-only: $R2"

exit $FAIL
