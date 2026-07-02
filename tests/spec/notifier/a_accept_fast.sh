#!/usr/bin/env bash
# A. 즉시 수락: 유효 이벤트 POST → 1초 이내 200 + status:accepted
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

out=$(ncurl "-s -w \"|%{http_code}|%{time_total}\" -X POST http://notifier:8080/api/notify \
  -H \"Content-Type: application/json\" \
  -d \"{\\\"siteId\\\":\\\"site1\\\",\\\"deviceId\\\":\\\"TEST-01\\\",\\\"type\\\":\\\"gas_leak\\\",\\\"timestamp\\\":\\\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\\\",\\\"test\\\":true}\"")
body=${out%%|*}; rest=${out#*|}; code=${rest%%|*}; t=${rest##*|}
echo "code=$code time=$t body=$body"
echo "$body" | grep -q "\"status\":\"accepted\"" && [ "$code" = 200 ] && awk "BEGIN{exit !($t<1.0)}" \
  && { echo OK; exit 0; } || { echo NOK; exit 1; }
