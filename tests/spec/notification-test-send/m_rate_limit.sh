#!/usr/bin/env bash
# M. 레이트리밋 (channel,target) 분당 1건 — 같은 쌍 2번째 → 429·발송 0; 다른 target 비-429.
# spec: docs/spec/notification-test-send.md — 단언 M (일반 · non-vacuous · 리미터 리셋 픽스처 전제)
# 리미터 리셋 픽스처(테스트모드/상태 초기화/재시작) 없이는 스위트 상호간섭 → 기본 SKIP.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
[ "${LIMITER_RESET:-0}" = "1" ] || skip "(부적절, no-fixture): (channel,target) 리미터 리셋 픽스처(LIMITER_RESET=1) 필요"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
post() { bcode -X POST -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d "{\"channel\":\"email\",\"target\":\"$1\"}" "$BACKEND/api/notifications/test"; }

a1=$(post "m-rl-a@example.com"); a2=$(post "m-rl-a@example.com"); b1=$(post "m-rl-b@example.com")
echo "same-target: 1st=$a1 2nd=$a2 | other-target=$b1"
[ "$a1" = "404" ] && skip "(미배포): POST /api/notifications/test 없음"
[ "$a2" = "429" ] || nok "같은 (email,target) 2번째 기대 429, 관측 $a2"
[ "$b1" != "429" ] || nok "다른 target이 429로 오거부됨(스코프 오류)"
ok "같은 (channel,target) 2번째 429 + 다른 target 비-429"
