#!/usr/bin/env bash
# F. rate limit — /auth/login 1분 내 11회 → 11번째 429
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: mutating — 프로덕션 로그인 rate limit 실소진 (운영자 로그인 일시 차단됨).
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
last=""
for i in $(seq 1 11); do
  last=$(bcode -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' \
    -d '{"username":"spectdd-nouser","password":"wrong"}')
done
echo "11th=$last"
[ "$last" = "429" ] && ok "429" || nok "기대 429, 관측 $last"
