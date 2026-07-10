#!/usr/bin/env bash
# E. 초대 자동 승인 — 유효 inviteToken 가입 → active, invitations.status → accepted
# spec: docs/spec/web-backend.md — 검증 단언 (TDD)
# SKIP: mutating — 초대 생성(이메일 발송 유발) + 계정 생성.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
tok=$(bcurl -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"email":"spectdd-e@example.invalid"}' "$BACKEND/api/invitations" | jq -r .token)
U="spectdd-e-$(date +%s)"
# NOTE(harness R3): register/login share one recycled-RemoteAddr rate bucket
#   since SEC-3(704814d) neutralised X-Forwarded-For isolation. We judge the
#   invite→auto-active *success* contract, so auth_body waits out a neighbouring
#   test's 429 (60s window) instead of false-NOK. Genuine 429/400 tests
#   (c01_5/c01_2/f_rate_limit) intentionally keep using bcode/bcurl.
st=$(auth_body -X POST "$BACKEND/auth/register" -H 'Content-Type: application/json' \
  -d "{\"username\":\"$U\",\"password\":\"secret123\",\"confirmPassword\":\"secret123\",\"name\":\"x\",\"inviteToken\":\"$tok\"}" | jq -r .status)
inv=$(db_query "SELECT status FROM invitations WHERE token='$tok'")
echo "user.status=$st invitation.status=$inv"
[ "$st" = "active" ] && [ "$inv" = "accepted" ] && ok "초대 자동 승인" || nok "불일치"
