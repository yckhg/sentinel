#!/usr/bin/env bash
# 계약3-4. 정상 생성 → 201, 응답에 streamKey 자동 포함
# spec: docs/spec/interface-web-api.md 계약 3
# SKIP: mutating — 실제 카메라 생성.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
out=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"name":"spectdd-key","sourceType":"rtsp","sourceUrl":"rtsp://cam.example.com/s","enabled":false}' "$BACKEND/api/cameras")
echo "$out" | jq -c .
id=$(echo "$out" | jq -r '.id // empty'); key=$(echo "$out" | jq -r '.streamKey // empty')
[ -n "$id" ] && bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/cameras/$id" >/dev/null  # cleanup
[ -n "$key" ] && ok "streamKey=$key 발급" || nok "streamKey 미포함"
