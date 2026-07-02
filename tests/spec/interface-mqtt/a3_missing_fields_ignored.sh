#!/usr/bin/env bash
# A-3. 필수 필드 누락 → 무시 (OK: incident 미생성 + hw-gateway 생존)
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

$PUB -q 2 -t "safety/site1/alert" -m "{\"deviceId\":\"SPEC-02\",\"siteId\":\"site1\"}"
sleep 3
cnt=$(db_query "SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-02';")
state=$(docker inspect -f "{{.State.Status}}" sentinel-hw-gateway)
echo "incidents(SPEC-02)=$cnt, hw-gateway=$state"
[ "$cnt" -eq 0 ] && [ "$state" = running ] && { echo OK; exit 0; } || { echo NOK; exit 1; }
