#!/usr/bin/env bash
# K. status 채널 계약 — email·sms 각각 usable(bool)+미사용시 reason, 두 채널만.
#    (ENABLED 꺼짐+자격증명 조합 → usable=false 세부는 config 픽스처 필요.)
# spec: docs/spec/notification-test-send.md — 단언 K (일반 · non-vacuous)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"

out=$(bcurl_code -H "Authorization: Bearer $T" "$BACKEND/api/notifications/channels")
code=$(printf '%s' "$out" | tail -n1); body=$(printf '%s' "$out" | sed '$d')
echo "code=$code body=$(printf '%s' "$body" | head -c 250)"
[ "$code" = "404" ] && skip "(미배포): GET /api/notifications/channels 없음"
[ "$code" = "200" ] || nok "기대 200, 관측 $code"
# email·sms 각각 usable(boolean) 존재.
printf '%s' "$body" | jq -e '(.email.usable|type=="boolean") and (.sms.usable|type=="boolean")' >/dev/null \
  || nok "email/sms usable(boolean) 부재: $body"
# 정확히 두 채널만(email,sms) — 그 외 키 없음.
printf '%s' "$body" | jq -e '([keys[] | select(.=="email" or .=="sms")] | length)==(keys|length)' >/dev/null \
  && ok "email/sms usable+reason 계약 (두 채널만 노출)" \
  || nok "email/sms 외 채널 키가 노출됨: $(printf '%s' "$body" | jq -c 'keys')"
