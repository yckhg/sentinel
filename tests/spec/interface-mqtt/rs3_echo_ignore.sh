#!/usr/bin/env bash
# RS-3. 서버 echo 무시 (OK: kind=web 발행이 incident를 해소하지 않음)
# spec: docs/spec/interface-mqtt.md — 검증 단언 (TDD)
set -uo pipefail
db_query() {
  docker run --rm -v sentinel_db-data:/data:ro alpine:3.19 \
    sh -c 'apk add -q sqlite >/dev/null && sqlite3 -readonly /data/sentinel.db "$1"' sh "$1"
}
PUB="docker exec sentinel-mosquitto mosquitto_pub -h localhost"
SUB="docker exec sentinel-mosquitto mosquitto_sub -h localhost"
# SKIP: mutating — 프로덕션 실행 금지 (실제 incident 해소/장비 재시작 명령 유발).
if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED (mutating — 설계자 승인 대기): ALLOW_MUTATING=1 로만 실행"
  exit 2
fi

open_id=$(db_query "SELECT id FROM incidents WHERE site_id='site1' AND resolved_at IS NULL ORDER BY id DESC LIMIT 1;")
[ -n "$open_id" ] || { echo "SKIPPED: site1 미해결 incident 없음 (전제 미충족)"; exit 2; }
$PUB -q 1 -t "safety/site1/alert/resolved" \
  -m "{\"incidentId\":0,\"siteId\":\"site1\",\"resolvedAt\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"resolvedBy\":{\"kind\":\"web\",\"id\":\"admin\",\"label\":\"관리자\"}}"
sleep 3
ra=$(db_query "SELECT COALESCE(resolved_at,'NULL') FROM incidents WHERE id=$open_id;")
echo "incident $open_id resolved_at=$ra"
[ "$ra" = NULL ] && { echo "OK: echo 무시"; exit 0; } || { echo "NOK: echo가 resolve 유발"; exit 1; }
