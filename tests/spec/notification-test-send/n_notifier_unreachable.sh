#!/usr/bin/env bash
# N. notifier 도달불가 → 유한시간 내 502(+upstream_unavailable); not_configured/sent 강등 금지.
# spec: docs/spec/notification-test-send.md — 단언 N (핵심 · notifier 중단 픽스처 필요)
# notifier 중단/재시작 픽스처(예: docker compose stop notifier) 없이는 도달불가 경로를 못 태움.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
[ "${NOTIFIER_STOPPED:-0}" = "1" ] || skip "(부적절, no-fixture): notifier 중단/재시작 픽스처(NOTIFIER_STOPPED=1) 필요"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"

sc=$(bcode -H "Authorization: Bearer $T" "$BACKEND/api/notifications/channels")
out=$(bcurl_code -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"channel":"email","target":"n@example.com"}' "$BACKEND/api/notifications/test")
tc=$(printf '%s' "$out" | tail -n1); tb=$(printf '%s' "$out" | sed '$d')
echo "notifier down: status=$sc test=$tc body=$(printf '%s' "$tb" | head -c 200)"
[ "$sc" = "502" ] || nok "status 기대 502(미도달), 관측 $sc"
[ "$tc" = "502" ] || nok "test-send 기대 502(미도달), 관측 $tc"
printf '%s' "$tb" | jq -e '(.outcome=="not_configured" or .outcome=="sent")' >/dev/null \
  && nok "미도달을 outcome=$(printf '%s' "$tb" | jq -c '.outcome')로 강등" \
  || ok "notifier 미도달 → 502 (not_configured/sent 강등 없음)"
