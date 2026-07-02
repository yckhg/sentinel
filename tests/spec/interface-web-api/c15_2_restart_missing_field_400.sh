#!/usr/bin/env bash
# 계약15-2. hw-gateway POST /api/restart deviceId 누락 → 400 {"error":"siteId and deviceId are required"}
# spec: docs/spec/interface-web-api.md 계약 15
# SKIP: mutating — restart 발행 API 직접 타격 (구현 오류 시 MQTT restart 명령 발행 위험).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
out=$(bcurl_code -X POST -H 'Content-Type: application/json' -d '{"siteId":"site1"}' "$HWGW/api/restart")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1)
echo "code=$code body=$body"
[ "$code" = "400" ] && ok "400" || nok "기대 400, 관측 $code"
