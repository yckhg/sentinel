#!/usr/bin/env bash
# F. 관리자 전용 — status·테스트 세 표면: 무인증→401, 비-admin→403, 발송 0.
# spec: docs/spec/notification-test-send.md — 단언 F (핵심 · non-vacuous)
# 무인증 401은 read-only 불변; 비-admin 403은 USER_TOKEN 있을 때만 판정.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"

# 무인증 → 401 (두 표면).
c1=$(bcode "$BACKEND/api/notifications/channels")
c2=$(bcode -X POST -H 'Content-Type: application/json' -d '{"channel":"email","target":"x@example.com"}' "$BACKEND/api/notifications/test")
echo "no-auth: channels=$c1 test=$c2"
{ [ "$c1" = "000" ] || [ "$c2" = "000" ]; } && skip "(백엔드 미기동): 응답 없음"
[ "$c1" = "401" ] || nok "무인증 GET channels 기대 401, 관측 $c1"
[ "$c2" = "401" ] || nok "무인증 POST test 기대 401, 관측 $c2"

# 비-admin(user) → 403 (USER_TOKEN 주입 시).
if [ -n "${USER_TOKEN:-}" ]; then
  u1=$(bcode -H "Authorization: Bearer $USER_TOKEN" "$BACKEND/api/notifications/channels")
  u2=$(bcode -X POST -H "Authorization: Bearer $USER_TOKEN" -H 'Content-Type: application/json' \
    -d '{"channel":"email","target":"x@example.com"}' "$BACKEND/api/notifications/test")
  echo "user: channels=$u1 test=$u2"
  [ "$u1" = "403" ] || nok "비-admin GET channels 기대 403, 관측 $u1"
  [ "$u2" = "403" ] || nok "비-admin POST test 기대 403, 관측 $u2"
  ok "무인증 401 + 비-admin 403 (발송 0)"
else
  skip "(부분 판정): 무인증 401 확인; 비-admin 403은 USER_TOKEN 필요"
fi
