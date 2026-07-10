#!/usr/bin/env bash
# RS-2. 센서 버튼 해제 → incident 해소 (OK: resolved_at 갱신)
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

# 전제: site1에 미해결 incident 존재 (A-1 유형으로 생성). 최근 미해결 incident가 해소됨!
# NOTE(harness fix): incidentId=0 fallback 은 web-backend(incidents.go:512)에서
#   `status != 'resolved' ORDER BY datetime(occurred_at) DESC LIMIT 1` 로 대상을 고른다.
#   이전 버전은 `resolved_at IS NULL ORDER BY id DESC` 로 골라, 코드가 실제 해소하는 대상과
#   다른 incident 를 검사해 false-NOK 를 냈다(id 최신 ≠ occurred_at 최신). 코드와 동일한
#   기준으로 대상을 선택하도록 정렬/조건을 맞춘다.
open_id=$(db_query "SELECT id FROM incidents WHERE site_id='site1' AND status != 'resolved' ORDER BY datetime(occurred_at) DESC LIMIT 1;")
[ -n "$open_id" ] || { echo "SKIPPED: site1 미해결 incident 없음 (전제 미충족)"; exit 2; }
$PUB -q 1 -t "safety/site1/alert/resolved" \
  -m "{\"incidentId\":0,\"siteId\":\"site1\",\"resolvedAt\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"resolvedBy\":{\"kind\":\"sensor_button\",\"id\":\"SPEC-01\",\"label\":\"SPEC-01 reset 버튼\"}}"
sleep 3
ra=$(db_query "SELECT COALESCE(resolved_at,'NULL') FROM incidents WHERE id=$open_id;")
echo "incident $open_id resolved_at=$ra"
[ "$ra" != NULL ] && { echo OK; exit 0; } || { echo "NOK: resolved_at NULL 유지"; exit 1; }
