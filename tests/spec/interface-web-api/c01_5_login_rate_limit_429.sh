#!/usr/bin/env bash
# 계약1-5. 같은 IP에서 /auth/login 11회 연속 → 11번째 429
# spec: docs/spec/interface-web-api.md 계약 1
# SKIP: mutating — 프로덕션 로그인 rate limit을 실제로 소진시켜 운영자 로그인을 일시 차단함.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
last=""
for i in $(seq 1 11); do
  last=$(bcode -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' \
    -d '{"username":"spectdd-nouser","password":"wrongpass"}')
done
echo "11th code=$last"
[ "$last" = "429" ] && ok "11번째 429" || nok "기대 429, 관측 $last"
