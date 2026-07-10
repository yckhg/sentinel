#!/usr/bin/env bash
# 계약10-4. 초대 토큰으로 가입 → 201, status=="active" (계약 1 교차)
# spec: docs/spec/interface-web-api.md 계약 10
# SKIP: mutating — 초대 생성 + 계정 생성.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
tok=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"email":"spectdd-inv@example.invalid"}' "$BACKEND/api/invitations" | jq -r .token)
U="spectdd-inv-$(date +%s)"
# NOTE(harness R3): register/login share one recycled-RemoteAddr rate bucket
#   since SEC-3(704814d) neutralised X-Forwarded-For isolation. We judge the
#   invite→register *success* contract, so auth_body waits out a neighbouring
#   test's 429 (60s window) instead of false-NOK. Genuine 429/400 tests
#   (c01_5/c01_2/f_rate_limit) intentionally keep using bcode/bcurl.
out=$(auth_body -X POST "$BACKEND/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$U\",\"password\":\"secret123\",\"confirmPassword\":\"secret123\",\"name\":\"x\",\"inviteToken\":\"$tok\"}")
echo "$out" | jq -c '{status}'
[ "$(echo "$out" | jq -r .status)" = "active" ] && ok "초대 가입 즉시 active" || nok "status != active"
