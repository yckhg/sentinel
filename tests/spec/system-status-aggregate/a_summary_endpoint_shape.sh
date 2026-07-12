#!/usr/bin/env bash
# A/C/F. GET /api/health/summary — 집계 응답 shape:
#   summary{healthy,abnormal,offline} + services[] (healthy|unhealthy) +
#   exceptions[] (상한 50) + exceptionsOverflow. 합 불변식·서비스 완전성 관측.
# spec: docs/spec/system-status-aggregate.md — 단언 A/C/F, 출력(계약)
# read-only. 신규 엔드포인트가 아직 없으면 NOK(red) — 구현 대상.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"

out=$(bcurl_code -H "Authorization: Bearer $T" "$BACKEND/api/health/summary")
code=$(printf '%s' "$out" | tail -n1); body=$(printf '%s' "$out" | sed '$d')
echo "code=$code"; printf '%s' "$body" | jq -c '{summary, services_len:(.services|length), exceptions_len:(.exceptions|length), exceptionsOverflow}' 2>/dev/null | head -c 400; echo

[ "$code" = "200" ] || nok "GET /api/health/summary 기대 200, 관측 $code (신규 집계 엔드포인트 미구현?)"

# 요약 카운트 3범주 정수
printf '%s' "$body" | jq -e '.summary | (.healthy|type=="number") and (.abnormal|type=="number") and (.offline|type=="number")' >/dev/null \
  || nok "summary{healthy,abnormal,offline} 정수 3범주 아님"

# 서비스 목록: 비어있지 않고 각 항목 status ∈ {healthy,unhealthy}
printf '%s' "$body" | jq -e '.services | type=="array" and length>=1 and all(.[]; (.status=="healthy" or .status=="unhealthy"))' >/dev/null \
  || nok "services 목록이 계약 12 어휘(healthy|unhealthy)로 완전하지 않음"

# exceptions 상한 50, exceptionsOverflow 존재(정수)
printf '%s' "$body" | jq -e '(.exceptions|type=="array") and (.exceptions|length)<=50 and (.exceptionsOverflow|type=="number")' >/dev/null \
  || nok "exceptions 상한(<=50)/exceptionsOverflow 표식 위반"

# exceptions 항목 수 == min(abnormal+offline, 50), overflow == max(0, abnormal+offline-50)
ok_inv=$(printf '%s' "$body" | jq -e '
  (.summary.abnormal + .summary.offline) as $ex |
  (if $ex > 50 then 50 else $ex end) as $cap |
  ((.exceptions|length) == $cap) and (.exceptionsOverflow == (if $ex>50 then $ex-50 else 0 end))
' >/dev/null && echo 1 || echo 0)
[ "$ok_inv" = "1" ] || nok "경계 불변식 위반: exceptions len != min(예외수,50) 또는 overflow 불일치"

ok "집계 shape + 경계 불변식(len==min(예외,50), overflow) 관측"
