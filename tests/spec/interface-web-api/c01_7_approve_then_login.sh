#!/usr/bin/env bash
# 계약1-7. POST /auth/approve/{id} 후 해당 계정 로그인 → 200
# spec: docs/spec/interface-web-api.md 계약 1
# SKIP: mutating — 계정 생성 + 승인 상태 변경 + 로그인 반복.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
U="spectdd-appr-$(date +%s)"
id=$(bcurl -X POST "$BACKEND/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$U\",\"password\":\"secret123\",\"confirmPassword\":\"secret123\",\"name\":\"x\"}" | jq -r .id)
bcurl -X POST -H "Authorization: Bearer $T" "$BACKEND/auth/approve/$id" >/dev/null
code=$(bcode -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$U\",\"password\":\"secret123\"}")
echo "login code=$code"
[ "$code" = "200" ] && ok "승인 후 로그인 200" || nok "기대 200, 관측 $code"
