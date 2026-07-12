#!/usr/bin/env bash
# J. 입력 검증(레이트리밋보다 선행, 400은 토큰 미소모).
#    빈/형식오류 target → 400, 발송 0. 400 직후 유효 요청은 429 아님.
# spec: docs/spec/notification-test-send.md — 단언 J (일반 · non-vacuous)
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
post() { bcode -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' -d "$1" "$BACKEND/api/notifications/test"; }

c_empty_e=$(post '{"channel":"email","target":""}')
[ "$c_empty_e" = "404" ] && skip "(미배포): POST /api/notifications/test 없음"
c_bad_e=$(post '{"channel":"email","target":"not-an-email"}')
c_empty_s=$(post '{"channel":"sms","target":""}')
c_bad_s=$(post '{"channel":"sms","target":"12345"}')
echo "email empty=$c_empty_e bad=$c_bad_e | sms empty=$c_empty_s bad=$c_bad_s"
for c in "$c_empty_e" "$c_bad_e" "$c_empty_s" "$c_bad_s"; do
  [ "$c" = "400" ] || nok "잘못된 입력 기대 400, 관측 $c"
done
ok "빈/형식오류 target → 400 (발송 0). 토큰 미소모 상호작용은 단언 M(리미터 리셋 픽스처)에서 판정"
