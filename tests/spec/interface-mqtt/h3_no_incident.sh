#!/usr/bin/env bash
# H-3. heartbeat는 incident를 만들지 않음 (OK: incidents 0) — read-only
# spec: docs/spec/interface-mqtt.md — 검증 단언 (TDD)
set -uo pipefail
db_query() {
  docker run --rm -v sentinel_db-data:/data:ro alpine:3.19 \
    sh -c 'apk add -q sqlite >/dev/null && sqlite3 -readonly /data/sentinel.db "$1"' sh "$1"
}
PUB="docker exec sentinel-mosquitto mosquitto_pub -h localhost"
gw_status() { docker exec sentinel-hw-gateway wget -q -O- http://localhost:8080/api/equipment/status; }

# read-only: DB 조회만. 단 전제(H-1 실행으로 SPEC-HB-01 존재)가 필요.
dev=$(db_query "SELECT COUNT(*) FROM devices WHERE device_id='SPEC-HB-01';")
if [ "$dev" -eq 0 ]; then echo "SKIPPED: 전제(H-1) 미충족 — SPEC-HB-01 미등록 (판정 무의미)"; exit 2; fi
cnt=$(db_query "SELECT COUNT(*) FROM incidents WHERE device_id='SPEC-HB-01';")
echo "incidents(SPEC-HB-01)=$cnt"
[ "$cnt" -eq 0 ] && { echo OK; exit 0; } || { echo "NOK: heartbeat가 incident 생성"; exit 1; }
