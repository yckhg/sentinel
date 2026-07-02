#!/usr/bin/env bash
# RS-5. (펌웨어) 4단계 idempotency — 동일 resolved 2회 수신에도 정상
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

# 관측 불가 주의: 판정에는 물리 디바이스(LED/부저/디스플레이) 관측이 필요 — 서버 측에서 자동 판정 불가.
$PUB -q 1 -t "safety/site1/alert/resolved" \
  -m "{\"incidentId\":0,\"siteId\":\"site1\",\"resolvedAt\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"resolvedBy\":{\"kind\":\"sensor_button\",\"id\":\"SPEC-01\",\"label\":\"SPEC-01 reset 버튼\"}}"
$PUB -q 1 -t "safety/site1/alert/resolved" \
  -m "{\"incidentId\":0,\"siteId\":\"site1\",\"resolvedAt\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"resolvedBy\":{\"kind\":\"sensor_button\",\"id\":\"SPEC-01\",\"label\":\"SPEC-01 reset 버튼\"}}"
echo "발행 2회 완료 — 디바이스 현장 관측 필요: LED/부저 OFF 유지, 에러 없음, 이후 새 alert 감지 가능"
echo "MANUAL: 자동 판정 불가 (펌웨어/물리 관측 대상)"; exit 2
