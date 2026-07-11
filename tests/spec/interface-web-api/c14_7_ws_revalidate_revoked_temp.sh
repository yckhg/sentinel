#!/usr/bin/env bash
# 계약14-7. temp 링크 토큰으로 /ws 접속 성립 후 admin 이 링크 DELETE → N(≤60s)+여유 내
#   서버가 WS 를 능동 종료(close/EOF), 이후 crisis_alert 미수신. (수립 연결 주기적 재검증)
# spec: docs/spec/interface-web-api.md 계약 14 (접속 후 주기적 재검증 — 회수 반영)
# SKIP: mutating — temp 링크 발급/회수 + 최대 ~75초 WS 관측. ALLOW_MUTATING=1 로만.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
body=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d '{"label":"spec-c14_7"}' "$BACKEND/api/links/temp")
tok=$(echo "$body" | jq -r .token); id=$(echo "$body" | jq -r .id)
{ [ -n "$tok" ] && [ "$tok" != "null" ]; } || nok "temp 링크 발급 실패"
log="$SPEC_TMP/c14_7_ws.log"
ws_observe "/ws?token=$tok" 75 normal > "$log" & wpid=$!
sleep 4
if ! grep -q '^HTTP: HTTP/1.1 101' "$log"; then
  kill $wpid 2>/dev/null || true
  bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/links/$id" >/dev/null 2>&1 || true
  nok "WS 연결 미성립"
fi
# 링크 회수 → 재검증 주기 내 능동 종료 기대
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/links/$id" >/dev/null
wait $wpid
grep -vE '^TEXT: .*(crisis_alert|incident_resolved)' "$log" | head -10
end=$(grep -E '^(CLOSE|EOF):' "$log" | head -1)
[ -n "$end" ] || nok "회수 후 재검증 주기(≤60s)+여유 내 서버측 능동 종료 미관측 (재검증 미구현 의심)"
t=$(echo "$end" | sed -E 's/.*: ([0-9.]+)s/\1/')
awk -v t="$t" 'BEGIN{exit !(t<=70)}' && ok "회수 temp WS ${t}s 후 능동 종료" || nok "종료 시점 ${t}s (기대 ≤70s = 회수시점+재검증 상한 60s+여유)"
