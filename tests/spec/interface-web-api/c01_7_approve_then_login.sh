#!/usr/bin/env bash
# 계약1-7. POST /auth/approve/{id} 후 해당 계정 로그인 → 200
# spec: docs/spec/interface-web-api.md 계약 1
# SKIP: mutating — 계정 생성 + 승인 상태 변경 + 로그인 반복.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
U="spectdd-appr-$(date +%s)"
# NOTE(harness fix): auth 라우트의 rate-limit 은 clientIP(=X-Forwarded-For 첫 IP) 별 60초
#   버킷이다. 재사용되는 docker 컨테이너 IP 를 공유하면 선행 rate-limit 테스트(c01_4/c01_5)가
#   login 버킷을 소진해 이 테스트가 429 를 맞고 false-NOK 가 된다. 이 테스트 전용의 유니크
#   XFF 로 버킷을 분리해 승인→로그인 계약만 독립 검증한다.
XFF="10.$((RANDOM%200+20)).$((RANDOM%256)).$((RANDOM%254+1))"
id=$(bcurl -X POST "$BACKEND/auth/register" -H 'Content-Type: application/json' -H "X-Forwarded-For: $XFF" \
  -d "{\"username\":\"$U\",\"password\":\"secret123\",\"confirmPassword\":\"secret123\",\"name\":\"x\"}" | jq -r .id)
bcurl -X POST -H "Authorization: Bearer $T" "$BACKEND/auth/approve/$id" >/dev/null
code=$(bcode -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' -H "X-Forwarded-For: $XFF" \
  -d "{\"username\":\"$U\",\"password\":\"secret123\"}")
echo "login code=$code"
[ "$code" = "200" ] && ok "승인 후 로그인 200" || nok "기대 200, 관측 $code"
