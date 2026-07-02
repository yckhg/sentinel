#!/usr/bin/env bash
# 계약5-4. DELETE → 204, 재삭제 → 404
# spec: docs/spec/interface-web-api.md 계약 5
# SKIP: mutating — 연락처 생성/삭제.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
id=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"name":"spectdd-d","phone":"010-1234-5678","email":"","notifyEmail":false}' "$BACKEND/api/contacts" | jq -r .id)
c1=$(bcode -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/contacts/$id")
c2=$(bcode -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/contacts/$id")
echo "1st=$c1 2nd=$c2"
[ "$c1" = "204" ] && [ "$c2" = "404" ] && ok "204 → 404" || nok "1st=$c1 2nd=$c2"
