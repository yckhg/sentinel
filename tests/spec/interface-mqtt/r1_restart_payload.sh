#!/usr/bin/env bash
# R-1. restart 발행 형식 준수 (OK: 구독자가 필수 필드 완전한 JSON 1건 수신)
# spec: docs/spec/interface-mqtt.md — 검증 단언 (TDD)
set -uo pipefail
db_query() {
  docker run --rm -v sentinel_db-data:/data:ro alpine:3.19 \
    sh -c 'apk add -q sqlite >/dev/null && sqlite3 -readonly /data/sentinel.db "$1"' sh "$1"
}
PUB="docker exec sentinel-mosquitto mosquitto_pub -h localhost"
SUB="docker exec sentinel-mosquitto mosquitto_sub -h localhost"
# SKIP: mutating — 프로덕션 실행 금지 (실제 incident 해소/장비 재시작 명령 유발).
if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED (mutating — 설계자 승인 대기): ALLOW_MUTATING=1 로만 실행"
  exit 2
fi

# 필요 env: WEB_TOKEN (web-backend user 토큰), DEVICE_ID (site1의 devices 테이블 등록 장비)
: "${WEB_TOKEN:?WEB_TOKEN(user 토큰) 필요}"; : "${DEVICE_ID:?DEVICE_ID(등록 장비) 필요}"
out=/tmp/r1_sub.$$
timeout 15 $SUB -v -q 1 -W 12 -t "safety/site1/cmd/restart" > "$out" &
subpid=$!; sleep 2
# 웹 경로: web-backend /api/equipment/restart → hw-gateway → MQTT (장비가 실제 재시작됨!)
docker run --rm --network sentinel_sentinel-net alpine:3.19 sh -c \
  "apk add -q curl >/dev/null && curl -s -X POST http://web-backend:8080/api/equipment/restart \
   -H \"Authorization: Bearer $WEB_TOKEN\" -H \"Content-Type: application/json\" \
   -d \"{\\\"siteId\\\":\\\"site1\\\",\\\"deviceId\\\":\\\"$DEVICE_ID\\\",\\\"reason\\\":\\\"spec R-1\\\"}\""
wait $subpid; msg=$(cat "$out"); rm -f "$out"
echo "수신: $msg"
echo "$msg" | grep -q "\"deviceId\":\"$DEVICE_ID\"" && echo "$msg" | grep -q "\"siteId\":\"site1\"" \
  && echo "$msg" | grep -q "\"requestedBy\":\"[^\"]" && echo "$msg" | grep -q "\"timestamp\":\"[^\"]" \
  && { echo OK; exit 0; } || { echo "NOK: 미수신 또는 필수 필드 결손"; exit 1; }
