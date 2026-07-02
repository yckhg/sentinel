#!/usr/bin/env bash
# O. 브로커 다운 내성 (최초 연결 전): healthz 200 + restart 503 + 자동 복구
# spec: docs/spec/hw-gateway.md — 검증 단언 (TDD)
set -uo pipefail
db_query() {
  docker run --rm -v sentinel_db-data:/data:ro alpine:3.19 \
    sh -c 'apk add -q sqlite >/dev/null && sqlite3 -readonly /data/sentinel.db "$1"' sh "$1"
}
PUB="docker exec sentinel-mosquitto mosquitto_pub -h localhost"
SUB="docker exec sentinel-mosquitto mosquitto_sub -h localhost"
gw_get() { docker exec sentinel-hw-gateway wget -q -O- "http://localhost:8080$1"; }
gw_post() { # $1=path $2=json — busybox wget POST, 응답 본문 출력 (에러 시 stderr에 HTTP 코드)
  docker exec sentinel-hw-gateway wget -S -q -O- --header "Content-Type: application/json" \
    --post-data "$2" "http://localhost:8080$1" 2>&1
}
# SKIP: mutating — 프로덕션 실행 금지 (실제 알림 발송/incident 생성/장비 명령/컨테이너 조작 유발).
if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED (mutating — 설계자 승인 대기): ALLOW_MUTATING=1 로만 실행"
  exit 2
fi

# ⚠️ 매우 침습적: mosquitto 정지 + hw-gateway 재시작 — 모니터링 공백 발생. 설계자 승인 필수.
docker stop sentinel-mosquitto
docker restart sentinel-hw-gateway
sleep 5
h=$(docker exec sentinel-hw-gateway wget -S -q -O- http://localhost:8080/healthz 2>&1)
echo "$h" | grep -q "HTTP/1.1 200" || { echo "NOK: healthz != 200"; docker start sentinel-mosquitto; exit 1; }
r=$(gw_post /api/restart "{\"siteId\":\"site1\",\"deviceId\":\"T-O1\"}")
echo "restart resp=$r"
echo "$r" | grep -q "503" || { echo "NOK: 503 아님"; docker start sentinel-mosquitto; exit 1; }
docker start sentinel-mosquitto; sleep 10
$PUB -q 0 -t "safety/site1/heartbeat" -m "{\"deviceId\":\"T-O-B1\",\"siteId\":\"site1\"}"
sleep 3
gw_get /api/equipment/status | grep -q "T-O-B1" && { echo "OK: 자동 재연결·재구독 확인"; exit 0; } \
  || { echo "NOK: 재연결 후 B 단언 불성립"; exit 1; }
