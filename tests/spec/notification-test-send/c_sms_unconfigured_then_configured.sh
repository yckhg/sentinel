#!/usr/bin/env bash
# C. SMS 테스트 미설정→not_configured(무발송); 설정→발송 시도 1건(sent/failed).
# spec: docs/spec/notification-test-send.md — 단언 C
#   미설정 방향: non-vacuous(mutating). 설정 방향: SMS mock 공급자 픽스처 필요 → SKIP.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"

out=$(bcurl_code -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"channel":"sms","target":"010-1234-5678"}' "$BACKEND/api/notifications/test")
code=$(printf '%s' "$out" | tail -n1); body=$(printf '%s' "$out" | sed '$d')
echo "code=$code body=$(printf '%s' "$body" | head -c 200)"
[ "$code" = "404" ] && skip "(미배포): POST /api/notifications/test 없음"
[ "$code" = "200" ] || nok "미설정 방향 기대 200, 관측 $code"
printf '%s' "$body" | jq -e '.outcome=="not_configured"' >/dev/null \
  || nok "미설정 SMS outcome!=not_configured: $(printf '%s' "$body" | jq -c '.outcome')"
# 설정 방향(발송 시도 1건 → sent/failed)은 mock SMS 공급자 픽스처 없이 공허:
skip "(부적절, no-config/no-gateway): 미설정 방향 not_configured 확인; 설정 방향은 SMS mock 공급자 픽스처 필요"
