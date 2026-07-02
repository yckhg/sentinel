#!/usr/bin/env bash
# A-4. Malformed JSON → 무시 (OK: 크래시 없음 + 후속 처리 정상)
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

$PUB -q 2 -t "safety/site1/alert" -m "not-json{"
sleep 2
state=$(docker inspect -f "{{.State.Status}}" sentinel-hw-gateway)
[ "$state" = running ] || { echo "NOK: hw-gateway 상태=$state"; exit 1; }
aid="SPEC-01-A4-$(date +%s)"
before=$(db_query "SELECT COUNT(*) FROM incidents WHERE alert_id='$aid';")
$PUB -q 2 -t "safety/site1/alert" \
  -m "{\"deviceId\":\"SPEC-01\",\"siteId\":\"site1\",\"type\":\"scream\",\"alertId\":\"$aid\",\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
sleep 3
after=$(db_query "SELECT COUNT(*) FROM incidents WHERE alert_id='$aid';")
echo "hw-gateway running, 후속 alert incident: $before -> $after"
[ "$after" -gt "$before" ] && { echo OK; exit 0; } || { echo "NOK: 후속 처리 실패"; exit 1; }
