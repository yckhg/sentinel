#!/usr/bin/env bash
# 계약3-7. DELETE → 204, 이후 GET 목록에서 사라짐
# spec: docs/spec/interface-web-api.md 계약 3
# SKIP: mutating — 카메라 생성/삭제.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
id=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"name":"spectdd-del","sourceType":"rtsp","sourceUrl":"rtsp://cam.example.com/s","enabled":false}' "$BACKEND/api/cameras" | jq -r .id)
code=$(bcode -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/cameras/$id")
still=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/cameras" | jq "[.[] | select(.id==$id)] | length")
echo "delete code=$code remaining=$still"
[ "$code" = "204" ] && [ "$still" = "0" ] && ok "204 + 목록 제거" || nok "code=$code remaining=$still"
