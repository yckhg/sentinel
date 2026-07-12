#!/usr/bin/env bash
# G. 지원 채널 집합 = 정확히 {email, sms}; channel ∉ 집합(kakao) → 400, 발송 0.
# spec: docs/spec/notification-test-send.md — 단언 G (핵심 · non-vacuous)
# status(GET)는 read-only; kakao 거절(POST)은 mutating-경계이나 400은 발송 0.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"

out=$(bcurl_code -H "Authorization: Bearer $T" "$BACKEND/api/notifications/channels")
code=$(printf '%s' "$out" | tail -n1); body=$(printf '%s' "$out" | sed '$d')
echo "channels code=$code body=$(printf '%s' "$body" | head -c 200)"
[ "$code" = "404" ] && skip "(미배포): GET /api/notifications/channels 없음"
[ "$code" = "200" ] || nok "channels 기대 200, 관측 $code"
printf '%s' "$body" | jq -e 'has("email") and has("sms")' >/dev/null || nok "email/sms 채널 부재"
printf '%s' "$body" | jq -e 'keys | any(test("kakao";"i")) | not' >/dev/null || nok "KakaoTalk 채널이 노출됨"

# channel=kakao 테스트 발송 → 400 (발송 0).
kc=$(bcode -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"channel":"kakao","target":"x@example.com"}' "$BACKEND/api/notifications/test")
echo "kakao test code=$kc"
[ "$kc" = "400" ] && ok "채널 집합 {email,sms} + kakao 테스트 400" || nok "kakao 테스트 기대 400, 관측 $kc"
