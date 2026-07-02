#!/usr/bin/env bash
# 계약7-1. 미등록 deviceId로 restart → 400 (hw-gateway 호출 미발생)
# spec: docs/spec/interface-web-api.md 계약 7
# SKIP: mutating — 장비 restart 명령 API (구현 오류 시 실제 MQTT restart 발행 → 현장 릴레이 작동 위험).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
n=$(db_query "SELECT COUNT(*) FROM devices WHERE device_id='SPEC-NEVER-SEEN'")
[ "$n" = "0" ] || nok "전제 실패: SPEC-NEVER-SEEN 존재"
code=$(bcode -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"siteId":"site1","deviceId":"SPEC-NEVER-SEEN"}' "$BACKEND/api/equipment/restart")
echo "code=$code"
[ "$code" = "400" ] && ok "미등록 400" || nok "기대 400, 관측 $code"
