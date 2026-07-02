#!/usr/bin/env bash
# H-1. heartbeat → 장비 alive 등록 (OK: 상태 API alive=true + devices 영속)
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
  -m "{\"deviceId\":\"SPEC-HB-01\",\"siteId\":\"site1\",\"status\":\"running\",\"alertState\":\"none\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
sleep 3
api=$(gw_status)
echo "$api" | grep -q "\"deviceId\":\"SPEC-HB-01\",\"siteId\":\"site1\",\"alive\":true" || { echo "NOK: API에 alive=true 없음: $api"; exit 1; }
dev=$(db_query "SELECT COUNT(*) FROM devices WHERE device_id='SPEC-HB-01';")
echo "devices(SPEC-HB-01)=$dev"
[ "$dev" -ge 1 ] && { echo OK; exit 0; } || { echo "NOK: devices 미등록"; exit 1; }
