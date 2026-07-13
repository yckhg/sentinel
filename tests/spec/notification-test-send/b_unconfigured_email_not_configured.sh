#!/usr/bin/env bash
# B. 미설정 이메일 테스트 → outcome=not_configured (무발송·무크래시·유한 응답).
# spec: docs/spec/notification-test-send.md — 단언 B (핵심 · non-vacuous)
# mutating(테스트 발송) → ALLOW_MUTATING=1 로만 실행.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"

out=$(bcurl_code -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"channel":"email","target":"unconfigured@example.com"}' "$BACKEND/api/notifications/test")
code=$(printf '%s' "$out" | tail -n1); body=$(printf '%s' "$out" | sed '$d')
echo "code=$code body=$(printf '%s' "$body" | head -c 200)"
[ "$code" = "404" ] && skip "(미배포): POST /api/notifications/test 없음 — RED 게이트는 Go 단위테스트가 보유"
[ "$code" = "200" ] || nok "기대 200(유한 응답), 관측 $code"
printf '%s' "$body" | jq -e '.outcome=="not_configured"' >/dev/null \
  && ok "미설정 이메일 → not_configured (무발송·유한)" \
  || nok "outcome!=not_configured (거짓 성공/강등 의심): $(printf '%s' "$body" | jq -c '.outcome')"
