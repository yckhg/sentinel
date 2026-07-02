#!/usr/bin/env bash
# 계약3-5. PUT에 streamKey 포함 → 저장된 streamKey 불변
# spec: docs/spec/interface-web-api.md 계약 3
# SKIP: mutating — 카메라 생성/수정.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
out=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"name":"spectdd-imm","sourceType":"rtsp","sourceUrl":"rtsp://cam.example.com/s","enabled":false}' "$BACKEND/api/cameras")
id=$(echo "$out" | jq -r .id); key=$(echo "$out" | jq -r .streamKey)
bcurl -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"streamKey":"cam-hacked01"}' "$BACKEND/api/cameras/$id" >/dev/null
key2=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/cameras" | jq -r ".[] | select(.id==$id) | .streamKey")
bcurl -X DELETE -H "Authorization: Bearer $T" "$BACKEND/api/cameras/$id" >/dev/null  # cleanup
echo "before=$key after=$key2"
[ "$key" = "$key2" ] && ok "streamKey 불변" || nok "streamKey 변경됨"
