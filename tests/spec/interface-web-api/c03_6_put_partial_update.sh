#!/usr/bin/env bash
# 계약3-6. PUT {"name":"새이름"}만 → 다른 필드 유지 (partial)
# spec: docs/spec/interface-web-api.md 계약 3
# SKIP: mutating — 카메라 생성/수정.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
out=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"name":"spectdd-p1","location":"loc1","sourceType":"rtsp","sourceUrl":"rtsp://cam.example.com/s","enabled":false}' "$BACKEND/api/cameras")
id=$(echo "$out" | jq -r .id)
bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"name":"spectdd-p2"}' "$BACKEND/api/cameras/$id" >/dev/null
after=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/cameras" | jq -c ".[] | select(.id==$id) | {name, location, sourceUrl}")
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/cameras/$id" >/dev/null  # cleanup
echo "$after"
echo "$after" | jq -e '.name=="spectdd-p2" and .location=="loc1" and .sourceUrl=="rtsp://cam.example.com/s"' >/dev/null \
  && ok "partial update 유지" || nok "다른 필드 소실"
