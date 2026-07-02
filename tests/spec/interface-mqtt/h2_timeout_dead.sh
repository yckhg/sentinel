#!/usr/bin/env bash
# H-2. 타임아웃 후 alive=false (OK: 35초 무발행 시 dead 마킹, row 유지)
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

# 전제: h1_heartbeat_alive.sh 실행 직후. 35초 대기.
echo "35초 대기 (HEARTBEAT_TIMEOUT_SEC=30 초과)..."; sleep 35
api=$(gw_status)
echo "$api" | grep -q "\"deviceId\":\"SPEC-HB-01\"" || { echo "NOK: 목록에서 사라짐"; exit 1; }
echo "$api" | grep -q "\"deviceId\":\"SPEC-HB-01\",\"siteId\":\"site1\",\"alive\":false" \
  && { echo "OK: alive=false + row 유지"; exit 0; } || { echo "NOK: 여전히 alive=true"; exit 1; }
