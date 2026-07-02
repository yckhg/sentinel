#!/usr/bin/env bash
# A-5. siteId 덮어쓰기 (OK: incident의 site_id가 토픽 값 site1)
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

$PUB -q 2 -t "safety/site1/alert" \
  -m "{\"deviceId\":\"SPEC-03\",\"siteId\":\"WRONG-SITE\",\"type\":\"scream\",\"alertId\":\"SPEC-03-A5\",\"timestamp\":\"2026-07-02T10:05:00Z\"}"
sleep 3
sid=$(db_query "SELECT site_id FROM incidents WHERE device_id='SPEC-03' ORDER BY id DESC LIMIT 1;")
echo "site_id=$sid"
[ "$sid" = site1 ] && { echo OK; exit 0; } || { echo "NOK: site_id=$sid (기대 site1)"; exit 1; }
