#!/usr/bin/env bash
# 계약3-1. sourceType:"http" 카메라 생성 → 400
# spec: docs/spec/interface-web-api.md 계약 3
# SKIP: mutating — POST /api/cameras (구현 오류 시 실제 카메라 생성 + adapter reload 유발).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
code=$(bcode -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"name":"spectdd","sourceType":"http","sourceUrl":"http://example.com/s","enabled":false}' "$BACKEND/api/cameras")
echo "code=$code"
[ "$code" = "400" ] && ok "400" || nok "기대 400, 관측 $code"
