#!/usr/bin/env bash
# O. 브로커 다운 내성 (최초 연결 전): healthz 503/degraded + restart 503 + 자동 복구
# spec: docs/spec/hw-gateway.md — 검증 단언 O · §헬스체크(healthz) 계약 · §MQTT 발행 API 공통 응답 계약
#
# #51 계약: 브로커 미연결(최초 연결 전)이면 /healthz 는 503 "status":"degraded" (200 아님).
# 발행 엔드포인트(restart)는 최초 연결 미성립 시 503 "MQTT broker not connected" 즉시 반환.
# 이후 mosquitto 기동 시 별도 조치 없이 자동 재연결·재구독으로 B 단언 성립.
set -uo pipefail
NET=${NET:-sentinel_sentinel-net}
CURL=${CURL_IMG:-curlimages/curl:latest}
PUB="docker exec sentinel-mosquitto mosquitto_pub -h localhost"
gw_get() { docker exec sentinel-hw-gateway wget -q -O- "http://localhost:8080$1" 2>/dev/null; }
# in-network curl — 503 에서도 exit 0. stdout: "<body>\n<http_code>".
gw_probe() { docker run --rm --network "$NET" "$CURL" -s --max-time "${2:-3}" \
  -w '\n%{http_code}' "http://hw-gateway:8080$1" 2>/dev/null; }
gw_post()  { docker run --rm --network "$NET" "$CURL" -s --max-time 10 -X POST \
  -H 'Content-Type: application/json' -w '\n%{http_code}' -d "$2" "http://hw-gateway:8080$1" 2>/dev/null; }

# SKIP: mutating — 프로덕션 실행 금지 (컨테이너 조작·모니터링 공백 유발).
if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED (mutating — 설계자 승인 대기): ALLOW_MUTATING=1 로만 실행"
  exit 2
fi

# ── 복원 보장 (F2): 어떤 실패 경로에서도 종료 시 mosquitto 재기동 + hw-gateway healthz 200/ok 회복 폴링 ──
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

# ⚠️ 매우 침습적: mosquitto 정지 + hw-gateway 재시작 — 모니터링 공백. 설계자 승인 필수.
docker stop sentinel-mosquitto >/dev/null
docker restart sentinel-hw-gateway >/dev/null

# HTTP 서버 자체는 기동·응답한다(브로커 무관). 응답 가능해질 때까지 폴링(최대 20s).
code=""
for _ in $(seq 1 20); do
  resp=$(gw_probe /healthz 2); code=$(printf '%s' "$resp" | tail -n1)
  [ -n "$code" ] && [ "$code" != 000 ] && break
  sleep 1
done
body=$(printf '%s' "$resp" | sed '$d')
echo "healthz(최초연결전): code=$code body=$body"
[ "$code" = 503 ] || { echo "NOK: 최초 연결 전 healthz != 503 (code=$code) — #51 degraded 계약 위반"; exit 1; }
printf '%s' "$body" | grep -q '"status":"degraded"' || { echo "NOK: healthz 본문에 \"status\":\"degraded\" 없음"; exit 1; }

# restart 발행 → 최초 연결 미성립이므로 503 (즉시)
rresp=$(gw_post /api/restart "{\"siteId\":\"site1\",\"deviceId\":\"T-O1\"}")
rcode=$(printf '%s' "$rresp" | tail -n1); rbody=$(printf '%s' "$rresp" | sed '$d')
echo "restart: code=$rcode body=$rbody"
[ "$rcode" = 503 ] || { echo "NOK: restart 503 아님 (code=$rcode)"; exit 1; }

# 복구: mosquitto 기동 → 자동 재연결·재구독 → B 단언 (heartbeat 등록)
docker start sentinel-mosquitto >/dev/null
# healthz 200/ok 회복 대기 (재연결·재구독)
for _ in $(seq 1 30); do
  hc=$(gw_probe /healthz 2); [ "$(printf '%s' "$hc" | tail -n1)" = 200 ] && break
  sleep 1
done
$PUB -q 0 -t "safety/site1/heartbeat" -m "{\"deviceId\":\"T-O-B1\",\"siteId\":\"site1\"}"
sleep 3
if gw_get /api/equipment/status | grep -q "T-O-B1"; then
  echo "OK: 최초연결전 503/degraded + restart 503 + 자동 재연결·재구독(B) 확인"
  exit 0
else
  echo "NOK: 재연결 후 B 단언 불성립 (T-O-B1 미등록)"; exit 1
fi
