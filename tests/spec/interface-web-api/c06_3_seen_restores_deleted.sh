#!/usr/bin/env bash
# 계약6-3. soft-deleted device에 POST /api/devices/seen → /api/devices 재등장 (deletedAt null)
# spec: docs/spec/interface-web-api.md 계약 6 (계약 13 교차)
# SKIP: mutating — devices 테이블 변경.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
bcurl -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","deviceId":"SPEC-RES-01"}' "$BACKEND/api/devices/seen" >/dev/null
id=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/devices" | jq -r '.[] | select(.deviceId=="SPEC-RES-01") | .id')
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/devices/$id" >/dev/null
bcurl -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","deviceId":"SPEC-RES-01"}' "$BACKEND/api/devices/seen" >/dev/null
row=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/devices" | jq -c ".[] | select(.id==$id)")
echo "$row"
echo "$row" | jq -e '.deletedAt == null' >/dev/null && ok "자동 복원" || nok "복원 안 됨"
