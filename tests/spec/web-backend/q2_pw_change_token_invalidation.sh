#!/usr/bin/env bash
# Q2. 비밀번호 변경 시 토큰 무효화 — 토큰 t 로 GET /api/incidents 200 → change-password →
#   같은 t 401 → 재로그인 토큰 200 → 컨테이너 재시작 후에도 t 여전히 401(경계 users.password_changed_at
#   DB 영속 — 단언 Q 와 대칭) → 다른 사용자 V 의 변경-전 토큰은 영향 없음(200).
# spec: docs/spec/web-backend.md 단언 Q2 / interface-web-api.md 계약 1
# SKIP: mutating — 계정 2개 생성/승인 + 비밀번호 변경 + web-backend 재시작(ALLOW_MUTATING 하). ALLOW_MUTATING=1 로만.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
TS=$(date +%s)
U="spectdd-q2u-$TS-$$"; V="spectdd-q2v-$TS-$$"; PW="secret123"; NPW="secret456"
uid=$(auth_body -X POST "$BACKEND/auth/register" -H 'Content-Type: application/json' -d "{\"username\":\"$U\",\"password\":\"$PW\",\"confirmPassword\":\"$PW\",\"name\":\"U\"}" | jq -r .id)
vid=$(auth_body -X POST "$BACKEND/auth/register" -H 'Content-Type: application/json' -d "{\"username\":\"$V\",\"password\":\"$PW\",\"confirmPassword\":\"$PW\",\"name\":\"V\"}" | jq -r .id)
bcurl -X POST -H "Authorization: Bearer $T" "$BACKEND/auth/approve/$uid" >/dev/null
bcurl -X POST -H "Authorization: Bearer $T" "$BACKEND/auth/approve/$vid" >/dev/null
t=$(auth_body -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"$U\",\"password\":\"$PW\"}" | jq -r .token)
tv=$(auth_body -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"$V\",\"password\":\"$PW\"}" | jq -r .token)
{ [ -n "$t" ] && [ "$t" != "null" ] && [ -n "$tv" ] && [ "$tv" != "null" ]; } || nok "U/V 로그인 토큰 획득 실패"

c_before=$(bcode -H "Authorization: Bearer $t" "$BACKEND/api/incidents")
cp=$(bcode -X POST -H "Authorization: Bearer $t" -H 'Content-Type: application/json' -d "{\"currentPassword\":\"$PW\",\"newPassword\":\"$NPW\"}" "$BACKEND/api/auth/change-password")
c_after=$(bcode -H "Authorization: Bearer $t" "$BACKEND/api/incidents")
t2=$(auth_body -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"$U\",\"password\":\"$NPW\"}" | jq -r .token)
c_relogin=$(bcode -H "Authorization: Bearer $t2" "$BACKEND/api/incidents")
c_v=$(bcode -H "Authorization: Bearer $tv" "$BACKEND/api/incidents")
echo "before=$c_before change-pw=$cp after=$c_after relogin=$c_relogin V=$c_v"
[ "$c_before" = "200" ]  || nok "변경 전 t 로 incidents != 200 ($c_before)"
[ "$c_after"  = "401" ]  || nok "변경 후 같은 t 가 무효화되지 않음 ($c_after, 기대 401)"
[ "$c_relogin" = "200" ] || nok "재로그인 토큰이 유효하지 않음 ($c_relogin)"
[ "$c_v"      = "200" ]  || nok "무관 사용자 V 토큰이 영향받음 ($c_v, 기대 200)"

# 경계 DB 영속 확인 — 재시작 후에도 변경-이전 토큰 t 는 계속 401
docker restart sentinel-web-backend >/dev/null && sleep 8
c_after_restart=$(bcode -H "Authorization: Bearer $t" "$BACKEND/api/incidents")
echo "after-restart t=$c_after_restart (기대 401)"
[ "$c_after_restart" = "401" ] || nok "재시작 후 변경-이전 토큰 t 부활 ($c_after_restart, 기대 401 — 경계 in-memory 의심)"
ok "비밀번호 변경 토큰 무효화 + 재시작 생존 + V 무영향"
