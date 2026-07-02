#!/usr/bin/env bash
# H-5. alertState 누락 → none 보정 (OK: alive=true + alertState=none)
# spec: docs/spec/interface-mqtt.md — 검증 단언 (TDD)
set -uo pipefail
db_query() {
  docker run --rm -v sentinel_db-data:/data:ro alpine:3.19 \
    sh -c 'apk add -q sqlite >/dev/null && sqlite3 -readonly /data/sentinel.db "$1"' sh "$1"
}
PUB="docker exec sentinel-mosquitto mosquitto_pub -h localhost"
gw_status() { docker exec sentinel-hw-gateway wget -q -O- http://localhost:8080/api/equipment/status; }
# SKIP: mutating — 프로덕션 실행 금지 (실제 incident 생성/알림 발송/장비 명령 유발).
if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED (mutating — 설계자 승인 대기): ALLOW_MUTATING=1 로만 실행"
  exit 2
fi

$PUB -q 0 -t "safety/site1/heartbeat" \
  -m "{\"deviceId\":\"SPEC-HB-02\",\"siteId\":\"site1\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
sleep 3
api=$(gw_status)
entry=$(echo "$api" | grep -o "{\"deviceId\":\"SPEC-HB-02\"[^}]*}")
echo "entry=$entry"
echo "$entry" | grep -q "\"alive\":true" && echo "$entry" | grep -q "\"alertState\":\"none\"" \
  && { echo OK; exit 0; } || { echo "NOK: 무시되었거나 alertState != none"; exit 1; }
