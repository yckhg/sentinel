#!/usr/bin/env bash
# C. 최후 보루 시도 + 실패 로그: 알람 시도 결과 로그 건수 == 연락처 수
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

# 전제: KAKAO_ENABLED/SMS_ENABLED != true (현 스택 기본). 실제 이메일도 발송됨에 주의.
en=$(docker inspect sentinel-notifier --format "{{range .Config.Env}}{{println .}}{{end}}" | grep -E "KAKAO_ENABLED=true|SMS_ENABLED=true" || true)
[ -z "$en" ] || { echo "SKIPPED: 외부 채널 활성 상태 — 전제 불일치"; exit 2; }
ncontacts=$(db_query "SELECT COUNT(*) FROM contacts WHERE deleted_at IS NULL;" 2>/dev/null || db_query "SELECT COUNT(*) FROM contacts;")
[ "$ncontacts" -ge 1 ] || { echo "SKIPPED: 연락처 0건 — 전제 미충족"; exit 2; }
# SINCE = 주입 직전 절대 타임스탬프. `--since 30s` 상대창은 순차 실행 시 직전 이벤트(A/B)의
# "System alarm" 로그를 계수에 섞어 건수 불일치 오판(false-NOK)을 낸다. 이 주입 이후의
# 로그만 보도록 시간창을 고정한다.
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
ncurl "-s -o /dev/null -X POST http://notifier:8080/api/notify -H \"Content-Type: application/json\" \
  -d \"{\\\"siteId\\\":\\\"site1\\\",\\\"deviceId\\\":\\\"TEST-C1\\\",\\\"type\\\":\\\"gas_leak\\\",\\\"timestamp\\\":\\\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\\\",\\\"test\\\":true}\""
sleep 8
alarms=$(docker logs sentinel-notifier --since "$SINCE" 2>&1 | grep -c "System alarm")
echo "연락처=$ncontacts 알람시도로그=$alarms"
[ "$alarms" = "$ncontacts" ] && { echo OK; exit 0; } || { echo "NOK: 건수 불일치"; exit 1; }
