#!/usr/bin/env bash
# C-3. 핵심 필드 누락 → 무시 (OK: device 미등록)
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

$PUB -q 0 -t "safety/site1/event/candidate" -m "{\"deviceId\":\"SPEC-CD-02\",\"siteId\":\"site1\"}"
sleep 3
cnt=$(db_query "SELECT COUNT(*) FROM devices WHERE device_id='SPEC-CD-02';")
echo "devices(SPEC-CD-02)=$cnt"
[ "$cnt" -eq 0 ] && { echo OK; exit 0; } || { echo "NOK: 무시돼야 할 메시지가 처리됨"; exit 1; }
