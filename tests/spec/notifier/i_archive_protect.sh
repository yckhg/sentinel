#!/usr/bin/env bash
# I. 녹화 보호 트리거: 유효 이벤트 → 보호 요청 수락 로그 (incident_{siteId}_...)
# spec: docs/spec/notifier.md — 검증 단언 (TDD)
set -uo pipefail
db_query() {
  docker run --rm -v sentinel_db-data:/data:ro alpine:3.19 \
    sh -c 'apk add -q sqlite >/dev/null && sqlite3 -readonly /data/sentinel.db "$1"' sh "$1"
}
# Docker 내부망에서 notifier로 직접 주입 (curl은 일회성 컨테이너에 설치 — 호스트 미오염)
ncurl() {
  docker run --rm --network sentinel_sentinel-net alpine:3.19 sh -c \
    "apk add -q curl >/dev/null && curl $*"
}
# SKIP: mutating — 프로덕션 실행 금지 (실제 이메일 발송/시스템 알람 시도/녹화 보호 요청 유발).
if [ "${ALLOW_MUTATING:-0}" != "1" ]; then
  echo "SKIPPED (mutating — 설계자 승인 대기): ALLOW_MUTATING=1 로만 실행"
  exit 2
fi

cams=$(db_query "SELECT COUNT(*) FROM cameras WHERE enabled=1;" 2>/dev/null || echo 0)
[ "$cams" -ge 1 ] || { echo "SKIPPED: 활성 카메라 0대 — 전제 미충족"; exit 2; }
ncurl "-s -o /dev/null -X POST http://notifier:8080/api/notify -H \"Content-Type: application/json\" \
  -d \"{\\\"siteId\\\":\\\"site1\\\",\\\"deviceId\\\":\\\"TEST-I1\\\",\\\"type\\\":\\\"gas_leak\\\",\\\"timestamp\\\":\\\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\\\",\\\"test\\\":true}\""
sleep 10
docker logs sentinel-notifier --since 40s 2>&1 | grep -E "\[archive\] Protect request accepted for incident incident_site1_.*\([0-9]+ cameras\)" \
  && { echo OK; exit 0; } || { echo "NOK: 보호 요청 수락 로그 없음"; exit 1; }
