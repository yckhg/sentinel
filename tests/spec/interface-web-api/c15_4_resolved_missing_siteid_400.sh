#!/usr/bin/env bash
# 계약15-4. POST /api/alert/resolved siteId 누락 → 400
# spec: docs/spec/interface-web-api.md 계약 15
# SKIP: mutating — alert/resolved 발행 API 직접 타격 (구현 오류 시 MQTT 해소 발행 → 센서 LED 등 동기화 오작동).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
code=$(bcode -X POST -H 'Content-Type: application/json' -d '{"incidentId":1}' "$HWGW/api/alert/resolved")
echo "code=$code"
[ "$code" = "400" ] && ok "400" || nok "기대 400, 관측 $code"
