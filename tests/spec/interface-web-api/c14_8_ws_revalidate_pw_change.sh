#!/usr/bin/env bash
# 계약14-8. 로그인-JWT WS 접속 성립 후 소유 사용자 비밀번호 변경 → N(≤60s)+여유 내
#   서버가 WS 를 능동 종료(자격증명 경계 iat < password_changed_at — 계약 1·14).
# spec: docs/spec/interface-web-api.md 계약 14 (접속 후 주기적 재검증 — 비밀번호 변경 경계)
# SKIP: mutating — 계정 생성/승인 + WS 접속 + 비밀번호 변경 + 최대 ~75초 관측. ALLOW_MUTATING=1 로만.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
require_mutating
T=$(get_token) || exit 1
TS=$(date +%s); U="spectdd-c148-$TS-$$"; PW="secret123"; NPW="secret456"
uid=$(auth_body -X POST "$BACKEND/auth/register" -H 'Content-Type: application/json' -d "{\"username\":\"$U\",\"password\":\"$PW\",\"confirmPassword\":\"$PW\",\"name\":\"U\"}" | jq -r .id)
bcurl -X POST -H "Authorization: Bearer $T" "$BACKEND/auth/approve/$uid" >/dev/null
t=$(auth_body -X POST "$BACKEND/auth/login" -H 'Content-Type: application/json' -d "{\"username\":\"$U\",\"password\":\"$PW\"}" | jq -r .token)
{ [ -n "$t" ] && [ "$t" != "null" ]; } || nok "U 로그인 토큰 획득 실패"
log="$SPEC_TMP/c14_8_ws.log"
ws_observe "/ws?token=$t" 75 normal > "$log" & wpid=$!
sleep 4
grep -q '^HTTP: HTTP/1.1 101' "$log" || { kill $wpid 2>/dev/null || true; nok "WS 연결 미성립"; }
# 비밀번호 변경 → 자격증명 경계 초과 → 재검증 주기 내 능동 종료 기대
cp=$(bcode -X POST -H "Authorization: Bearer $t" -H 'Content-Type: application/json' -d "{\"currentPassword\":\"$PW\",\"newPassword\":\"$NPW\"}" "$BACKEND/api/auth/change-password")
echo "change-password=$cp"
wait $wpid
grep -vE '^TEXT: .*(crisis_alert|incident_resolved)' "$log" | head -10
end=$(grep -E '^(CLOSE|EOF):' "$log" | head -1)
[ -n "$end" ] || nok "비번 변경 후 재검증 주기(≤60s)+여유 내 서버측 능동 종료 미관측 (재검증 미구현 의심)"
t2=$(echo "$end" | sed -E 's/.*: ([0-9.]+)s/\1/')
awk -v t="$t2" 'BEGIN{exit !(t<=70)}' && ok "비번 변경 후 WS ${t2}s 후 능동 종료" || nok "종료 시점 ${t2}s (기대 ≤70s)"
