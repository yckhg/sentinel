#!/usr/bin/env bash
# 계약3-2. youtube + https://youtube.com/live/abc123 생성 → 201
# spec: docs/spec/interface-web-api.md 계약 3
# SKIP: mutating — 실제 카메라 생성 + adapter reload. (성공 시 즉시 DELETE로 정리해야 함)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
out=$(bcurl_code -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"name":"spectdd-yt","sourceType":"youtube","sourceUrl":"https://youtube.com/live/abc123","enabled":false}' "$BACKEND/api/cameras")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1); echo "code=$code body=$body"
id=$(echo "$body" | jq -r '.id // empty')
[ -n "$id" ] && bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/cameras/$id" >/dev/null  # cleanup
[ "$code" = "201" ] && ok "201" || nok "기대 201, 관측 $code"
