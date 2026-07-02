#!/usr/bin/env bash
# L. 센서 해소 forward: sensor_button → resolve-from-sensor 1회, unknown kind → forward 없음
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

$PUB -q 1 -t "safety/site1/alert/resolved" \
  -m "{\"incidentId\":0,\"siteId\":\"site1\",\"resolvedAt\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"resolvedBy\":{\"kind\":\"sensor_button\",\"id\":\"T-L1\",\"label\":\"reset\"}}"
sleep 4
docker logs sentinel-hw-gateway --since 20s 2>&1 | grep -qi "resolve" || { echo "NOK: forward 로그 없음"; exit 1; }
$PUB -q 1 -t "safety/site1/alert/resolved" \
  -m "{\"incidentId\":0,\"siteId\":\"site1\",\"resolvedAt\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"resolvedBy\":{\"kind\":\"unknown\",\"id\":\"x\",\"label\":\"x\"}}"
sleep 4
docker logs sentinel-hw-gateway --since 8s 2>&1 | grep -q "resolve-from-sensor" \
  && { echo "NOK: unknown kind가 forward됨"; exit 1; } || { echo OK; exit 0; }
