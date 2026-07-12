#!/usr/bin/env bash
# I. 센서 버튼 해소 경로 — open 경보에 POST /api/incidents/{id}/resolve-from-sensor(유효 바디)
#    → 200, resolvedByKind==sensor_button, 이후 status==resolved. open → resolved 직접 전이.
# spec: docs/spec/alarm-history-lifecycle.md — 단언 I (핵심, mutating·terminal)
# 순서의존: 마이그레이션 게이트 M(acknowledged 0건) 적용 후 폴백(status='open')이 open 만 매칭.
# 기본 SKIP: 새 open incident 시딩 후 종단 전이(공유 DB 오염).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
SID="ahl-i-$(date +%s%N)-$$-${RANDOM}"
id=$(bcurl -X POST -H 'Content-Type: application/json' \
  -d "{\"siteId\":\"$SID\",\"description\":\"i-open\",\"isTest\":true}" \
  "$BACKEND/api/incidents" | jq -r .id)
[ -n "$id" ] && [ "$id" != "null" ] || nok "seed open incident 실패 (id=$id)"
out=$(bcurl_code -X POST -H 'Content-Type: application/json' \
  -d "{\"incidentId\":$id,\"siteId\":\"$SID\",\"resolvedBy\":{\"kind\":\"sensor_button\",\"id\":\"btn-1\",\"label\":\"현장 버튼\"}}" \
  "$BACKEND/api/incidents/$id/resolve-from-sensor")
code=$(printf '%s' "$out" | tail -n1); body=$(printf '%s' "$out" | sed '$d')
st=$(db_query "SELECT status FROM incidents WHERE id=$id")
echo "id=$id code=$code body=$body db_status=$st"
[ "$code" = "200" ] && printf '%s' "$body" | jq -e '.resolvedByKind=="sensor_button"' >/dev/null && [ "$st" = "resolved" ] \
  && ok "센서 해소 open → resolved(sensor_button)" || nok "code=$code body=$body db=$st"
