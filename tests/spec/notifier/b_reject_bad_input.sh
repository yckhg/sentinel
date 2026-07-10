#!/usr/bin/env bash
# B. 불량 입력 거절: siteId 누락 → 400 + 채널 발송 시도 없음
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

# 주의: 검증 로직이 스펙과 다르면(NOK 케이스) 실제 발송이 일어날 수 있어 mutating으로 분류.
# SINCE = 주입 직전 절대 타임스탬프. `--since 15s` 상대창은 순차 실행 시 직전 이벤트(A)의
# [email]/Dispatch 채널 로그를 섞어 "발송 시도 없음" 판정을 오염(false-NOK)시킨다.
# 채널 로그는 deviceId 를 담지 않으므로 이 주입 이후의 로그만 보도록 시간창을 고정한다.
SINCE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
code=$(ncurl "-s -o /dev/null -w \"%{http_code}\" -X POST http://notifier:8080/api/notify \
  -H \"Content-Type: application/json\" \
  -d \"{\\\"deviceId\\\":\\\"TEST-01\\\",\\\"type\\\":\\\"gas_leak\\\",\\\"timestamp\\\":\\\"2026-07-02T00:00:00Z\\\"}\"")
sleep 3
sent=$(docker logs sentinel-notifier --since "$SINCE" 2>&1 | grep -cE "\[email\]|\[sms\]|\[kakao\]|Dispatch")
echo "code=$code 채널로그=$sent"
[ "$code" = 400 ] && [ "$sent" = 0 ] && { echo OK; exit 0; } || { echo NOK; exit 1; }
