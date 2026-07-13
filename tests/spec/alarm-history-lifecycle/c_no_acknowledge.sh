#!/usr/bin/env bash
# C. acknowledge 전이 부재 — (1) GET /api/incidents 어떤 응답에도 status==acknowledged 없음
#    (read-only 판정), (2) PATCH /api/incidents/{id}/acknowledge 라우트 부재 → 404/405
#    (mutating 프로브: 라우트가 남아있으면 acknowledge 를 유발할 수 있어 ALLOW_MUTATING 가드).
# spec: docs/spec/alarm-history-lifecycle.md — 단언 C (핵심)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"

# (1) read-only: 목록에 acknowledged 값이 없어야 한다.
list=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/incidents?limit=100")
nack=$(printf '%s' "$list" | jq '[.data[]?|select(.status=="acknowledged")]|length' 2>/dev/null)
echo "acknowledged_in_list=$nack"
[ "$nack" = "0" ] || nok "목록에 acknowledged 원소 존재($nack)"

# (2) 라우트 부재 프로브 (mutating — 라우트가 남아있으면 실 incident 를 acknowledge 시도).
if [ "${ALLOW_MUTATING:-0}" = "1" ]; then
  SID="ahl-c-$(date +%s%N)-$$-${RANDOM}"
  id=$(bcurl -X POST -H 'Content-Type: application/json' \
    -d "{\"siteId\":\"$SID\",\"description\":\"c-open\",\"isTest\":true}" \
    "$BACKEND/api/incidents" | jq -r .id)
  code=$(bcode -X PATCH -H "Authorization: Bearer $T" "$BACKEND/api/incidents/$id/acknowledge")
  echo "acknowledge_route_code=$code"
  { [ "$code" = "404" ] || [ "$code" = "405" ]; } \
    && ok "acknowledged 값 부재 + acknowledge 라우트 부재($code)" \
    || nok "acknowledge 라우트가 200/기타로 응답: $code (라우트 미제거)"
else
  ok "acknowledged 값 부재(read-only). 라우트 부재 프로브는 ALLOW_MUTATING=1 로 별도 판정"
fi
