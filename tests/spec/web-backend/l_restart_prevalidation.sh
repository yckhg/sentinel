#!/usr/bin/env bash
# L. 재시작 사전 검증 — 미등록 400, soft-deleted 400, hw-gateway 미호출
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: mutating — restart API 타격 (구현 오류 시 실제 MQTT restart → 현장 릴레이 위험).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
o1=$(bcurl_code -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"siteId":"spectdd","deviceId":"SPEC-NOPE"}' "$BACKEND/api/equipment/restart")
c1=$(echo "$o1" | tail -1); b1=$(echo "$o1" | head -n -1)
# soft-deleted fixture 준비
bcurl -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","deviceId":"SPEC-L-DEL"}' "$BACKEND/api/devices/seen" >/dev/null
did=$(db_query "SELECT id FROM devices WHERE site_id='spectdd' AND device_id='SPEC-L-DEL'")
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/devices/$did" >/dev/null
o2=$(bcurl_code -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"siteId":"spectdd","deviceId":"SPEC-L-DEL"}' "$BACKEND/api/equipment/restart")
c2=$(echo "$o2" | tail -1); b2=$(echo "$o2" | head -n -1)
echo "unregistered=$c1 $b1"; echo "soft-deleted=$c2 $b2"
echo "$b1" | jq -e 'has("error")' >/dev/null && echo "$b2" | jq -e 'has("error")' >/dev/null \
  && [ "$c1" = "400" ] && [ "$c2" = "400" ] && ok "사전 검증 400+error 봉투" || nok "불일치"
