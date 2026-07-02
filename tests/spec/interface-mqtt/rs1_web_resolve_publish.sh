#!/usr/bin/env bash
# RS-1. 웹 해제 → MQTT 발행 (OK: kind=web 메시지 1건 수신)
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

# 필요 env: ADMIN_TOKEN (admin 토큰), INCIDENT_ID (미해결 incident id — 실제로 해소 처리됨!)
: "${ADMIN_TOKEN:?ADMIN_TOKEN 필요}"; : "${INCIDENT_ID:?미해결 incident id 필요}"
out=/tmp/rs1_sub.$$
timeout 15 $SUB -v -q 1 -W 12 -t "safety/+/alert/resolved" > "$out" &
subpid=$!; sleep 2
docker run --rm --network sentinel_sentinel-net alpine:3.19 sh -c \
  "apk add -q curl >/dev/null && curl -s -X PATCH http://web-backend:8080/api/incidents/$INCIDENT_ID/resolve \
   -H \"Authorization: Bearer $ADMIN_TOKEN\" -H \"Content-Type: application/json\" \
   -d \"{\\\"resolutionNotes\\\":\\\"spec RS-1\\\"}\""
wait $subpid; msg=$(cat "$out"); rm -f "$out"
echo "수신: $msg"
echo "$msg" | grep -q "\"kind\":\"web\"" && { echo OK; exit 0; } || { echo "NOK: 미수신"; exit 1; }
