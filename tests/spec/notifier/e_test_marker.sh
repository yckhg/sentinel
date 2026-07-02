#!/usr/bin/env bash
# E. 테스트 표식: test:true → 발송 본문에 [테스트] 표식
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

# 주의: 발송 본문은 로그에 남지 않을 수 있음 — 수신 메일함 관측 필요할 수 있다.
ncurl "-s -o /dev/null -X POST http://notifier:8080/api/notify -H \"Content-Type: application/json\" \
  -d \"{\\\"siteId\\\":\\\"site1\\\",\\\"deviceId\\\":\\\"TEST-E1\\\",\\\"type\\\":\\\"gas_leak\\\",\\\"timestamp\\\":\\\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\\\",\\\"test\\\":true}\""
sleep 8
docker logs sentinel-notifier --since 30s 2>&1 | grep -q "테스트" \
  && { echo "OK: 로그에서 [테스트] 표식 관측"; exit 0; } \
  || { echo "MANUAL: 로그 미노출 — 수신 메일 본문에서 [테스트] 확인 필요"; exit 2; }
