#!/usr/bin/env bash
# R-2. 미등록 device 거부 — 웹 경로 (OK: 400 + MQTT 미발행)
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

# 필요 env: WEB_TOKEN. 미등록 deviceId 는 자동 생성.
: "${WEB_TOKEN:?WEB_TOKEN(user 토큰) 필요}"
dev="NEVER-SEEN-$(date +%s)"
out=/tmp/r2_sub.$$
timeout 12 $SUB -v -q 1 -W 10 -t "safety/site1/cmd/restart" > "$out" &
subpid=$!; sleep 2
code=$(docker run --rm --network sentinel_sentinel-net alpine:3.19 sh -c \
  "apk add -q curl >/dev/null && curl -s -o /dev/null -w \"%{http_code}\" -X POST \
   http://web-backend:8080/api/equipment/restart \
   -H \"Authorization: Bearer $WEB_TOKEN\" -H \"Content-Type: application/json\" \
   -d \"{\\\"siteId\\\":\\\"site1\\\",\\\"deviceId\\\":\\\"$dev\\\"}\"")
wait $subpid; msg=$(cat "$out"); rm -f "$out"
echo "HTTP=$code, MQTT 수신=[$msg]"
[ "$code" = 400 ] && [ -z "$msg" ] && { echo OK; exit 0; } || { echo "NOK: 400 아님 또는 발행됨"; exit 1; }
