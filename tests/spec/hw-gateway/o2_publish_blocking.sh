#!/usr/bin/env bash
# O2. 브로커 단절 중 발행 타임아웃 503 (연결 성립 후): 유한시간 내 503 + healthz 200
# spec: docs/spec/hw-gateway.md — 검증 단언 (TDD)
# 설계자 승인 변경: 이전엔 무기한 발행 블로킹(curl exit 28)을 기대했으나,
# 발행 대기를 5초 타임아웃으로 제한하고 타임아웃 시 503 반환하도록 변경됨.
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

# ⚠️ 매우 침습적: mosquitto 정지. 또한 재기동 시 큐 적재된 restart가 실제 발행됨(T-O1 장비 명령) 주의.
docker stop sentinel-mosquitto
set +e
# --max-time 10 = 발행 타임아웃(5s) + 여유. HTTP 상태코드만 캡처.
code=$(docker run --rm --network sentinel_sentinel-net alpine:3.19 sh -c \
  "apk add -q curl >/dev/null && curl --max-time 10 -s -o /dev/null -w '%{http_code}' -X POST http://hw-gateway:8080/api/restart \
   -H \"Content-Type: application/json\" -d \"{\\\"siteId\\\":\\\"site1\\\",\\\"deviceId\\\":\\\"T-O1\\\"}\"")
rc=$?
set -e
h=$(docker exec sentinel-hw-gateway wget -S -q -O- http://localhost:8080/healthz 2>&1)
docker start sentinel-mosquitto
echo "curl rc=$rc http_code=$code (기대: rc=0, code=503 — 유한시간 내 503, hang 아님)"
echo "$h" | grep -q "HTTP/1.1 200" || { echo "NOK: healthz != 200"; exit 1; }
[ "$rc" = 28 ] && { echo "NOK: 무기한 hang (curl timeout) — 유한 503 기대"; exit 1; }
[ "$code" = 503 ] && { echo OK; exit 0; } || { echo "NOK: 503 아님 (code=$code)"; exit 1; }
