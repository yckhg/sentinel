#!/usr/bin/env bash
# E. 드릴다운 — GET /api/health/events?entity_id=<siteId:deviceId> 는 그 장비의
#    sensor 전이만 반환하고 다른 장비 전이는 하나도 포함하지 않는다.
# spec: docs/spec/system-status-aggregate.md — 단언 E / 계약 12 델타
# read-only. 기존 sensor health_events 를 fixture 로 사용.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"

# 존재하는 sensor 전이 이력에서 대상 entity_id 하나를 고른다.
eid=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/health/events?entity_kind=sensor&limit=100" \
  | jq -r 'map(.entityId) | first // empty')
[ -n "$eid" ] || skip "(fixture 부재): sensor 전이 이력 없음 (장비 online/offline 전이 기록 필요)"
echo "target entity_id=$eid"

# entity_id 필터로 조회
enc=$(printf '%s' "$eid" | jq -sRr @uri)
out=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/health/events?entity_id=$enc&limit=100")
n=$(printf '%s' "$out" | jq 'length')
echo "rows=$n"
[ "$n" -ge 1 ] || nok "entity_id 필터 매칭 0행 (센서 entityId 사상 실패 — 매칭 0 아님이어야)"

# 모든 행이 그 장비(entityId==eid)이며 kind==sensor. 다른 장비 전이 0건.
printf '%s' "$out" | jq -e --arg id "$eid" 'all(.[]; .entityId==$id and .entityKind=="sensor")' >/dev/null \
  && ok "${n}행 전부 $eid 의 sensor 전이 (타 장비 전이 0건)" \
  || nok "다른 장비/kind 전이가 섞여 있음 (드릴다운 스코프 위반)"
