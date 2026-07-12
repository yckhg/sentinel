#!/usr/bin/env bash
# 계약2-8. GET /api/incidents/active — 200 배열, 각 원소 status ∈ {open,acknowledged},
#   incidentId + 중첩 site.{address,managerName,managerPhone} 존재, crisis_alert(계약 14) payload 와
#   키 동형(incidentId,siteId,description,occurredAt,isTest,site.* — status 만 추가 허용), 발생시각 내림차순.
# spec: docs/spec/interface-web-api.md 계약 2 (/api/incidents/active 배너 backfill)
# read-only(GET). 미해결 사고 0건이면 vacuous — 원소 수 n 보고(verifier 가 n==0 시 SKIPPED 오버레이).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
out=$(bcurl_code -H "Authorization: Bearer $T" "$BACKEND/api/incidents/active")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code"
[ "$code" = "200" ] || nok "GET /api/incidents/active != 200 ($code — 접면 미구현 의심)"
echo "$body" | jq -e 'type=="array"' >/dev/null || nok "응답이 배열 아님"
n=$(echo "$body" | jq 'length')
echo "INFO: /active 원소 수 n=$n (n==0 이면 내용 단언 vacuous — verifier SKIPPED 오버레이 대상)"

# 상태집합 + 필수 키 + site 중첩
echo "$body" | jq -e 'all(.[]; (.status=="open" or .status=="acknowledged")
  and has("incidentId") and has("siteId") and has("description") and has("occurredAt") and has("isTest")
  and (.site|has("address") and has("managerName") and has("managerPhone")))' >/dev/null \
  || nok "원소 스키마 위반 (status집합/incidentId/site.* 결손)"

# 키 동형: crisis_alert payload 6키 전부 존재 + 잉여 키 없음(status 만 허용) + site 정확히 3키
echo "$body" | jq -e 'all(.[];
  ((["incidentId","siteId","description","occurredAt","isTest","site"] - keys_unsorted) == [])
  and ((keys_unsorted - ["incidentId","siteId","description","occurredAt","isTest","site","status"]) == [])
  and (.site | (["address","managerName","managerPhone"] - keys_unsorted) == []
             and (keys_unsorted - ["address","managerName","managerPhone"]) == []))' >/dev/null \
  || nok "키 집합이 crisis_alert payload 와 비동형(잉여/결손 키 — 반쪽 배너 위험)"

# 발생시각 내림차순
echo "$body" | jq -e '[.[].occurredAt] == ([.[].occurredAt]|sort|reverse)' >/dev/null \
  || nok "occurredAt 내림차순 아님"

[ "$n" = "0" ] && skip "(no-data): /active 200·배열 형태 OK 이나 미해결 사고 0건 — 내용 단언 vacuous"
ok "/active 200·동형·상태집합·내림차순 (n=$n)"
