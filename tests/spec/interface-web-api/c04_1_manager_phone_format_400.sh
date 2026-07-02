#!/usr/bin/env bash
# 계약4-1. managerPhone:"02-123-4567" PUT → 400
# spec: docs/spec/interface-web-api.md 계약 4
# SKIP: mutating — PUT /api/sites (구현 오류 시 실제 현장 정보 변경).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
id=$(bcurl -H "Authorization: Bearer $T" "$BACKEND/api/sites" | jq -r '.[0].id // empty')
[ -n "$id" ] || skip "(fixture 부재): sites 비어 있음"
code=$(bcode -X PUT -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"managerPhone":"02-123-4567"}' "$BACKEND/api/sites/$id")
echo "code=$code"
[ "$code" = "400" ] && ok "400" || nok "기대 400, 관측 $code"
