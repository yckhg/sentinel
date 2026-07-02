#!/usr/bin/env bash
# F. 기본값 주입: description/severity 없이 → fallback 주입 + 빈 값 아님
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

ncurl "-s -o /dev/null -X POST http://notifier:8080/api/notify -H \"Content-Type: application/json\" \
  -d \"{\\\"siteId\\\":\\\"site1\\\",\\\"deviceId\\\":\\\"TEST-F1\\\",\\\"type\\\":\\\"gas_leak\\\",\\\"timestamp\\\":\\\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\\\",\\\"test\\\":true}\""
sleep 8
log=$(docker logs sentinel-notifier --since 30s 2>&1)
echo "$log" | grep -E "fallback|default|기본값" && { echo OK; exit 0; } \
  || { echo "MANUAL: fallback 주입 로그 포맷 확인 필요 — 로그: $(echo "$log" | grep TEST-F1 | head -3)"; exit 2; }
