#!/usr/bin/env bash
# 계약13-9. POST /api/incidents에 deviceId 포함 → GET /api/devices에 등장 (UPSERT)
# spec: docs/spec/interface-web-api.md 계약 13
# SKIP: mutating — incident + device 생성.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
bcurl -X POST -H 'Content-Type: application/json' \
  -d '{"siteId":"spectdd","deviceId":"SPEC-UPS-01","description":"t","isTest":true}' "$BACKEND/api/incidents" >/dev/null
sleep 1
n=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/devices" | jq '[.[] | select(.deviceId=="SPEC-UPS-01")] | length')
echo "device rows=$n"
[ "$n" = "1" ] && ok "UPSERT 확인" || nok "device 미등장"
