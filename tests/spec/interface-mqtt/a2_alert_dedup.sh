#!/usr/bin/env bash
# A-2. 동일 alertId 재전송 → incident 중복 없음 (OK: 카운트 불변)
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

# 전제: a1_alert_incident.sh 직후 실행 (동일 alertId SPEC-01-A1 재발행)
before=$(db_query "SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-01';")
$PUB -q 2 -t "safety/site1/alert" \
  -m "{\"deviceId\":\"SPEC-01\",\"siteId\":\"site1\",\"type\":\"scream\",\"alertId\":\"SPEC-01-A1\",\"timestamp\":\"2026-07-02T10:00:00Z\",\"severity\":\"critical\"}"
sleep 3
after=$(db_query "SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-01';")
echo "incidents(SPEC-01): $before -> $after"
[ "$after" -eq "$before" ] && { echo "OK: dedup — 카운트 불변"; exit 0; } || { echo "NOK: 중복 incident 생성"; exit 1; }
