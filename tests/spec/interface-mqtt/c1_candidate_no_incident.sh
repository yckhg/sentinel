#!/usr/bin/env bash
# C-1. 유효 candidate → device 등록만, incident 없음
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

$PUB -q 0 -t "safety/site1/event/candidate" \
  -m "{\"deviceId\":\"SPEC-CD-01\",\"siteId\":\"site1\",\"type\":\"voice_candidate\",\"class\":\"save_me\",\"confidence\":0.61,\"threshold\":0.80,\"timestamp\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
sleep 3
res=$(db_query "SELECT (SELECT COUNT(*) FROM devices WHERE device_id='SPEC-CD-01') || '|' || (SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-CD-01');")
dev=${res%%|*}; inc=${res##*|}
echo "devices=$dev incidents=$inc"
[ "$dev" -ge 1 ] && [ "$inc" -eq 0 ] && { echo OK; exit 0; } || { echo NOK; exit 1; }
