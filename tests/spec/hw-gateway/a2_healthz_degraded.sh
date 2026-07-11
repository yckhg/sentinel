#!/usr/bin/env bash
# A2. 헬스체크 degraded (MQTT 미연결/미구독): /healthz → 503 "status":"degraded" (1s 이내),
#     재연결·재구독 시 200 "status":"ok"로 회복.
# spec: docs/spec/hw-gateway.md — 검증 단언 A2 · §헬스체크(healthz) 계약
#
# 구조: (read-only) healthy 계약 + <1s 응답상한 불변식을 먼저 단언한다. degraded 전이는
#       mosquitto 정지를 요하므로 require_mutating 뒤에 둔다 — 스모크(ALLOW_MUTATING=0)에선 SKIP.
# 참고: healthy 본문은 {"status":"ok"}만 노출하며 경보성 3토픽(alert/heartbeat/resolved)의
#       SUBACK granted 여부를 별도 필드로 드러내지 않는다. 따라서 200/ok를 "3토픽 구독 성립"의
#       대리 관측으로 삼는다(§헬스체크 계약: ok는 연결+3토픽 granted일 때에만 반환).
set -uo pipefail
. "$(dirname "$0")/../lib-web.sh"

# /healthz 1회 프로브 — P_CODE/P_TIME/P_BODY 전역에 채운다 (서브셸 우회: 함수 직접 호출).
probe() {
  local out
  out=$(docker run --rm --network "$NET" "$CURL_IMG" -s --max-time 2 \
        -w '\nHTTPCODE=%{http_code} TIME=%{time_total}' "$HWGW/healthz" 2>/dev/null)
  local meta; meta=$(printf '%s' "$out" | tail -n1)
  P_BODY=$(printf '%s' "$out" | sed '$d')
  P_CODE=${meta#*HTTPCODE=}; P_CODE=${P_CODE%% *}
  P_TIME=${meta##*TIME=}
}
# 응답시간 < 1.0s 판정 (hang 금지 불변식)
under_1s() { awk -v t="$1" 'BEGIN{exit !(t+0<1.0 && t!="")}'; }

# --- read-only: healthy 계약 + <1s 불변식 -------------------------------------
probe
echo "healthy 프로브: code=$P_CODE time=${P_TIME}s body=$P_BODY"
[ "$P_CODE" = 200 ] || nok "healthy 상태에서 /healthz != 200 (code=$P_CODE) — 정상 계약 A 불성립"
printf '%s' "$P_BODY" | grep -q '"status":"ok"' || nok "본문에 \"status\":\"ok\" 없음 (3토픽 구독 성립 대리조건 불충족)"
under_1s "$P_TIME" || nok "healthz 응답 ${P_TIME}s ≥ 1s — hang 금지 불변식 위반 (in-memory 플래그 조회여야 함)"
echo "read-only OK: 200/ok + <1s 확인. degraded 전이는 mutating 게이트에서 검증."

# --- mutating: degraded 전이 + 회복 -------------------------------------------
require_mutating
# ⚠️ 매우 침습적: mosquitto 정지 → 전체 MQTT 모니터링 공백. 설계자 승인 필수.
restore() { docker start sentinel-mosquitto >/dev/null 2>&1 || true; }
trap restore EXIT
docker stop sentinel-mosquitto >/dev/null

# 단절 인지는 keep-alive/PING 경계 이내(§단절 감지 지연). 최대 8s 폴링하며 각 프로브 <1s 강제.
deg=0
for i in $(seq 1 8); do
  probe
  under_1s "$P_TIME" || nok "단절 중 healthz ${P_TIME}s ≥ 1s — 상태 플래그 조회가 블로킹됨(hang)"
  if [ "$P_CODE" = 503 ] && printf '%s' "$P_BODY" | grep -q '"status":"degraded"'; then deg=1; break; fi
  sleep 1
done
[ "$deg" = 1 ] || nok "브로커 단절 후 degraded(503/\"status\":\"degraded\") 미전이 — 경보 유실이 healthy로 은폐됨(RED)"
echo "degraded 전이 확인 (503/degraded, <1s)."

# 회복: mosquitto 재기동 → 자동 재연결·재구독 → 200/ok
docker start sentinel-mosquitto >/dev/null; trap - EXIT
rec=0
for i in $(seq 1 25); do
  probe
  if [ "$P_CODE" = 200 ] && printf '%s' "$P_BODY" | grep -q '"status":"ok"'; then rec=1; break; fi
  sleep 1
done
[ "$rec" = 1 ] && ok "healthy 계약 + <1s 불변식 + degraded 전이/회복(재연결·재구독) 확인" \
  || nok "재연결·재구독 후 healthy(200/ok) 회복 실패"
