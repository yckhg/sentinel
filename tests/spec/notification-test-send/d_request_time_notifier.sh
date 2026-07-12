#!/usr/bin/env bash
# D. 요청 시점 notifier 조회(web-backend 재시작 불요). 미설정→email.usable=false는
#    non-vacuous(read-only). 설정↔미설정 전환 + notifier 재기동 반영 방향은 픽스처 필요.
# spec: docs/spec/notification-test-send.md — 단언 D
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"

out=$(bcurl_code -H "Authorization: Bearer $T" "$BACKEND/api/notifications/channels")
code=$(printf '%s' "$out" | tail -n1); body=$(printf '%s' "$out" | sed '$d')
echo "code=$code body=$(printf '%s' "$body" | head -c 200)"
[ "$code" = "404" ] && skip "(미배포): GET /api/notifications/channels 없음"
[ "$code" = "200" ] || nok "기대 200, 관측 $code"
# 미설정 채널은 usable=false + reason 동반 (거짓 not_configured/이유없는 false 강등 금지).
printf '%s' "$body" | jq -e '(.email|has("usable")) and (.email|has("reason"))' >/dev/null \
  || nok "email 채널 usable/reason 필드 부재: $body"
# config-flip(설정↔미설정) + notifier 재기동으로 web-backend 재시작 없이 반영되는지는 픽스처 필요.
skip "(부적절, no-fixture): usable/reason 계약 확인; notifier config 전환+재기동 반영 방향은 픽스처 필요"
