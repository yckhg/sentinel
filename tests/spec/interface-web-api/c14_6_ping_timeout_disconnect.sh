#!/usr/bin/env bash
# 계약14-6. pong 미응답 클라이언트 → 약 40초 후 서버가 연결 종료
# spec: docs/spec/interface-web-api.md 계약 14
# read-only 관찰 (구독만, pong 미응답) — 실행 시간 최대 ~100초.
set -uo pipefail; . "$(dirname "$0")/../lib-web.sh"
T=$(get_token) || skip "(fixture 부재): admin 토큰 획득 실패"
out=$(ws_observe "/ws?token=$T" 100 noping)
echo "$out" | grep -vE '^TEXT: .*(crisis_alert|incident_resolved)' | head -10
end=$(echo "$out" | grep -E '^(CLOSE|EOF):' | head -1)
[ -n "$end" ] || nok "100초 내 서버측 종료 미관측 (TIMEOUT_END)"
t=$(echo "$end" | sed -E 's/.*: ([0-9.]+)s/\1/')
# 서버 ping 주기 30s + pong 대기 40s 설계 감안: 30~90s 내 종료면 OK
awk -v t="$t" 'BEGIN{exit !(t>=30 && t<=90)}' && ok "pong 미응답 시 ${t}s 후 종료" || nok "종료 시점 ${t}s (기대 30~90s)"
