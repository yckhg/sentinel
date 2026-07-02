#!/usr/bin/env bash
# 계약1-2. 7자 비밀번호 가입 → 400
# spec: docs/spec/interface-web-api.md 계약 1
# SKIP: mutating — POST /auth/register (검증 실패 기대지만 프로덕션 POST + register rate limit 소모).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
code=$(bcode -X POST "$BACKEND/auth/register" -H 'Content-Type: application/json' \
  -d '{"username":"spectdd-short","password":"1234567","confirmPassword":"1234567","name":"x"}')
echo "code=$code"
[ "$code" = "400" ] && ok "400" || nok "기대 400, 관측 $code"
