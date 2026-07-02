#!/usr/bin/env bash
# 계약13-6. devices/seen alertState:"active" → GET /api/devices에서 alertState=="active"
# spec: docs/spec/interface-web-api.md 계약 13 (계약 6 교차)
# SKIP: mutating — 장비 alertState 변경 (웹 화면에 경보 상태로 표시됨).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
bcurl -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","deviceId":"SPEC-AS-01","alertState":"active"}' "$BACKEND/api/devices/seen" >/dev/null
st=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/devices" | jq -r '.[] | select(.deviceId=="SPEC-AS-01") | .alertState')
# 원복
bcurl -X POST -H 'Content-Type: application/json' -d '{"siteId":"spectdd","deviceId":"SPEC-AS-01","alertState":"none"}' "$BACKEND/api/devices/seen" >/dev/null
echo "alertState=$st"
[ "$st" = "active" ] && ok "alertState 반영" || nok "관측 $st"
