#!/usr/bin/env bash
# 계약1-1. 정상 가입 → 201, status=="pending"
# spec: docs/spec/interface-web-api.md 계약 1
# SKIP: mutating — 프로덕션에 실제 계정을 생성함 (계정 생성 금지 정책).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
U="spectdd-$(date +%s)"
out=$(bcurl_code -X POST "$BACKEND/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$U\",\"password\":\"secret123\",\"confirmPassword\":\"secret123\",\"name\":\"SpecTDD\"}")
code=$(echo "$out" | tail -1); body=$(echo "$out" | head -n -1); echo "code=$code body=$body"
[ "$code" = "201" ] || nok "기대 201, 관측 $code"
[ "$(echo "$body" | jq -r .status)" = "pending" ] && ok "201 + pending" || nok "status != pending"
