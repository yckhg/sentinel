#!/usr/bin/env bash
# D. GET /api/incidents/active → 200, 모든 원소 status==open (resolved·acknowledged 미포함),
#    각 원소에 incidentId · site.address · site.managerName · site.managerPhone 존재.
# spec: docs/spec/alarm-history-lifecycle.md — 단언 D (핵심)
# read-only 판정. vacuous OK 방지를 위해 최소 1개 open 시딩은 ALLOW_MUTATING 하에서만.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"

if [ "${ALLOW_MUTATING:-0}" = "1" ]; then
  SID="ahl-d-$(date +%s%N)-$$-${RANDOM}"
  bcurl -X POST -H 'Content-Type: application/json' \
    -d "{\"siteId\":\"$SID\",\"description\":\"d-open\",\"isTest\":true}" \
    "$BACKEND/api/incidents" >/dev/null
fi

body=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents/active")
n=$(printf '%s' "$body" | jq 'length' 2>/dev/null)
echo "active_count=$n"
[ -n "$n" ] || nok "응답 파싱 실패: $body"
if [ "$n" = "0" ]; then
  skip "(vacuous 방지): active 비어있음 — 최소 1개 open 시딩 필요(ALLOW_MUTATING=1 재실행)"
fi
printf '%s' "$body" | jq -e 'all(.[]; .status=="open" and has("incidentId") and (.site|has("address") and has("managerName") and has("managerPhone")))' >/dev/null \
  && ok "active 전원 open + incidentId + site 연락정보" || nok "active 계약 불일치: $body"
