#!/usr/bin/env bash
# O2. 브로커 단절 중 발행 타임아웃 503 (연결 성립 후): 유한시간 내 503 + healthz 503/degraded
# spec: docs/spec/hw-gateway.md — 검증 단언 O2 · §MQTT 발행 API 공통 응답 계약 · §헬스체크 계약
#
# #51 계약: 연결이 한 번 성립한 뒤 mosquitto 정지 →
#   (a) restart 발행은 발행 타임아웃(5s)+여유 내에 503 "MQTT publish timeout — broker unreachable"
#       를 반환한다 (무기한 hang 아님 = curl exit 28/code 000 아님).
#   (b) /healthz 는 각 호출이 1s 이내 응답하며, keep-alive/PING 경계 이내에 503 "status":"degraded"
#       로 전이한다 (이전 stale 기대였던 200 아님).
set -uo pipefail
NET=${NET:-sentinel_sentinel-net}
CURL=${CURL_IMG:-curlimages/curl:latest}
gw_probe() { docker run --rm --network "$NET" "$CURL" -s --max-time "${2:-2}" \
  -w '\n%{http_code}' "http://hw-gateway:8080$1" 2>/dev/null; }
gw_post()  { docker run --rm --network "$NET" "$CURL" -s --max-time "${3:-10}" -X POST \
  -H 'Content-Type: application/json' -w '\n%{http_code}' -d "$2" "http://hw-gateway:8080$1" 2>/dev/null; }

# SKIP: mutating — 프로덕션 실행 금지 (컨테이너 조작·모니터링 공백 유발).
if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED (mutating — 설계자 승인 대기): ALLOW_MUTATING=1 로만 실행"
  exit 2
fi

# ── 복원 보장 (F2): 어떤 실패 경로에서도 종료 시 mosquitto 재기동 + healthz 200/ok 회복 폴링 ──
restore() {
  docker start sentinel-mosquitto >/dev/null 2>&1 || true
  for _ in $(seq 1 30); do
    c=$(docker run --rm --network "$NET" "$CURL" -s -o /dev/null -w '%{http_code}' \
        --max-time 2 "http://hw-gateway:8080/healthz" 2>/dev/null)
    [ "$c" = 200 ] && break
    sleep 1
  done
}
trap restore EXIT

# 전제: 연결이 한 번 성립한 상태(현 스택은 healthy). 확인 후 mosquitto 정지.
pre=$(gw_probe /healthz 2)
[ "$(printf '%s' "$pre" | tail -n1)" = 200 ] || { echo "NOK: 전제 불충족 — 정지 전 healthz != 200"; exit 1; }

# ⚠️ 매우 침습적: mosquitto 정지. 재기동 시 큐 적재된 restart가 실제 발행됨(T-O1 명령) 주의.
docker stop sentinel-mosquitto >/dev/null

# (a) restart 발행 → 발행 타임아웃(5s)+여유 내 유한 503. --max-time 10 로 hang(=code 000) 구별.
t0=$(date +%s)
rresp=$(gw_post /api/restart "{\"siteId\":\"site1\",\"deviceId\":\"T-O1\"}" 10)
t1=$(date +%s)
rcode=$(printf '%s' "$rresp" | tail -n1); rbody=$(printf '%s' "$rresp" | sed '$d')
echo "restart: code=$rcode elapsed=$((t1-t0))s body=$rbody (기대: 유한시간 내 503 publish timeout)"

# (b) healthz → keep-alive/PING 경계 이내(최대 15s) 503/degraded 전이. 각 프로브 <1s(1.5s 상한) 응답.
deg=0
for _ in $(seq 1 15); do
  hp=$(gw_probe /healthz 2); hc=$(printf '%s' "$hp" | tail -n1); hb=$(printf '%s' "$hp" | sed '$d')
  if [ "$hc" = 000 ]; then echo "NOK: healthz 무응답/hang (code=000) — in-memory 플래그 조회여야 함"; exit 1; fi
  if [ "$hc" = 503 ] && printf '%s' "$hb" | grep -q '"status":"degraded"'; then deg=1; break; fi
  sleep 1
done
echo "healthz(단절중): code=$hc body=$hb deg=$deg"

# 복원 (trap 도 보증하나 명시 복원 후 판정)
docker start sentinel-mosquitto >/dev/null

# ── 판정 ──
[ "$rcode" = 000 ] && { echo "NOK: 무기한 hang (curl timeout/code 000) — 유한 503 기대"; exit 1; }
[ "$rcode" = 503 ] || { echo "NOK: restart 503 아님 (code=$rcode)"; exit 1; }
printf '%s' "$rbody" | grep -q "MQTT publish timeout" \
  || { echo "NOK: restart 본문에 'MQTT publish timeout — broker unreachable' 없음 (body=$rbody)"; exit 1; }
[ "$deg" = 1 ] || { echo "NOK: 브로커 단절 후 keep-alive 경계 이내 healthz 503/degraded 미전이"; exit 1; }
echo "OK: 유한 503 publish timeout + healthz 503/degraded 전이 확인"
exit 0
