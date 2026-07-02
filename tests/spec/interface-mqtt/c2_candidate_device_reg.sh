#!/usr/bin/env bash
# C-2. candidate로 device 등록 (OK: devices row 존재) — read-only
# spec: docs/spec/interface-mqtt.md — 검증 단언 (TDD)
set -uo pipefail
db_query() {
  docker run --rm -v sentinel_db-data:/data:ro alpine:3.19 \
    sh -c 'apk add -q sqlite >/dev/null && sqlite3 -readonly /data/sentinel.db "$1"' sh "$1"
}
PUB="docker exec sentinel-mosquitto mosquitto_pub -h localhost"
gw_status() { docker exec sentinel-hw-gateway wget -q -O- http://localhost:8080/api/equipment/status; }

# read-only: DB 조회만. 전제 C-1 실행 필요.
dev=$(db_query "SELECT COUNT(*) FROM devices WHERE device_id='SPEC-CD-01';")
if [ "$dev" -eq 0 ]; then echo "SKIPPED: 전제(C-1) 미실행 — SPEC-CD-01 없음"; exit 2; fi
echo "devices(SPEC-CD-01)=$dev"; echo OK
