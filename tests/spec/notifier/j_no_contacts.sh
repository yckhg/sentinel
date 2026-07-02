#!/usr/bin/env bash
# J. 연락처 없음 시 종결: 0건 상태에서 200 accepted + 스킵 로그 + 채널 호출 없음
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

# ⚠️ 전제 조작 필요: 연락처 0건 상태 — 프로덕션 연락처 삭제는 금지. 스테이징에서 수행.
ncontacts=$(db_query "SELECT COUNT(*) FROM contacts;")
[ "$ncontacts" = 0 ] || { echo "SKIPPED: 연락처 ${ncontacts}건 존재 — 0건 전제를 프로덕션에서 만들 수 없음"; exit 2; }
out=$(ncurl "-s -w \"|%{http_code}\" -X POST http://notifier:8080/api/notify -H \"Content-Type: application/json\" \
  -d \"{\\\"siteId\\\":\\\"site1\\\",\\\"deviceId\\\":\\\"TEST-J1\\\",\\\"type\\\":\\\"gas_leak\\\",\\\"timestamp\\\":\\\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\\\",\\\"test\\\":true}\"")
code=${out##*|}
sleep 5
log=$(docker logs sentinel-notifier --since 20s 2>&1)
skip=$(echo "$log" | grep -ciE "no contacts|skip")
chan=$(echo "$log" | grep -cE "\[email\]|\[sms\]|\[kakao\]")
echo "code=$code skip로그=$skip 채널호출=$chan"
[ "$code" = 200 ] && [ "$skip" -ge 1 ] && [ "$chan" = 0 ] && { echo OK; exit 0; } || { echo NOK; exit 1; }
